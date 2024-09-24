package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/render"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"

	"github.com/sirupsen/logrus"
)

const (
	defaultExecTimeout = 120 * time.Second
)

type Proxy struct {
	client         *http.Client
	log            *logrus.Logger
	mon            monitoring.Observer
	allowedOrigins []string
	wsProxy        *WSProxyHandler
}

func NewProxy(l *logrus.Logger) *Proxy {
	hc := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 1,
			IdleConnTimeout:     defaultExecTimeout,
			Dial: (&net.Dialer{
				Timeout: 60 * time.Second,
			}).Dial,
		},
	}
	p := Proxy{
		client: hc,
		wsProxy: &WSProxyHandler{
			log:             l,
			messageMaxBytes: 15 * 1024 * 1024,
			client:          hc,
		},
		log:            l,
		allowedOrigins: []string{"*"},
	}
	go p.emitMetrics()
	return &p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b []byte
	ctx, cancel := context.WithTimeout(r.Context(), defaultExecTimeout)
	defer cancel()

	switch middleware.GetClientConnectionType(ctx) {
	case middleware.ConnectionTypeWebsocketCtx:
		upst, err := p.wsProxy.GetOrCreateUpstreamConnection(w, r)
		middleware.AddLogField(ctx, "upstream", upst.upstream)
		if err != nil {
			p.log.WithError(err).WithField("client", r.RemoteAddr).Warnf("connection with upstream failed")
			middleware.CancelConnection(ctx, err)
			return
		}

		if err = upst.UpstreamWrite(r); err != nil {
			p.log.WithError(err).WithField("client", upst.client).WithField("upstream", upst.upstream).Warnf("failed to pass request to upstream")
			middleware.CancelConnection(ctx, err)
			return
		}
		resp, err := upst.UpstreamRead(r)
		if err != nil {
			p.log.WithError(err).WithField("client", upst.client).WithField("upstream", upst.upstream).Warnf("failed to read upstreams response")
			middleware.CancelConnection(ctx, err)
			return
		}
		// send response back to client
		if _, err = w.Write(resp); err != nil {
			p.log.WithError(err).WithField("client", upst.client).WithField("upstream", upst.upstream).Warnf("unable to write response to client")
			middleware.CancelConnection(ctx, err)
			return
		}

	default:
		prov := middleware.GetEndpointProvider(ctx)
		server := prov.GetAliveEndpoint("http", 1)

		// rewrite client host and url with upstream data
		r.RequestURI = ""
		u, _ := url.Parse(server)
		r.URL = u
		r.Host = u.Host

		middleware.AddLogField(ctx, "upstream", server)

		proxyHeader := make(http.Header)
		middleware.CopyHeader(proxyHeader, r.Header)
		r.Header = proxyHeader
		upstreamResp, err := p.client.Do(r)
		if err != nil {
			render.Render(w, r, middleware.ErrorInternalServer(err))
			return
		}
		defer upstreamResp.Body.Close()
		middleware.CopyHeader(w.Header(), upstreamResp.Header)

		if b, err = io.ReadAll(upstreamResp.Body); err != nil {
			render.Render(w, r, middleware.ErrorInternalServer(err))
			return
		}

		jsonRpc := middleware.GetJsonRPCMiddleware(ctx)
		parsed := jsonRpc.ParsePayload(b)
		f, ok := parsed.(dto.RPCFrame)
		if ok && f.Error != nil {
			middleware.AddLogField(r.Context(), "error", fmt.Sprintf("%d: %s", f.Error.Code, f.Error.Message))
		}

		if _, err = w.Write(b); err != nil {
			p.log.WithError(err).WithField("client", r.RemoteAddr).WithField("upstream", server).Infof("unable to write response to client")
			return
		}
	}
}

func (p *Proxy) emitMetrics() {
	ticker := time.NewTicker(15 * time.Second)
	for range ticker.C {
		connectionsCount := make(map[string]int)
		requestsCount := make(map[string]int)
		p.wsProxy.connections.Range(func(key, value any) bool {
			upst := value.(*ProxiedConnection)

			host := endpoint.FormatUpstream(upst.upstream)
			connectionsCount[host]++
			if _, ok := requestsCount[host]; !ok {
				requestsCount[host] = 0
			}
			upst.requests.Range(func(key, value any) bool {
				requestsCount[host]++
				return true
			})
			return true
		})
		for host, c := range connectionsCount {
			p.mon.ProcessEvent(monitoring.MetricProxyEstablishedConnections, float64(c), host)
		}
		for host, c := range requestsCount {
			p.mon.ProcessEvent(monitoring.MetricProxyRunningRequests, float64(c), host)
		}
	}
}

func (p *Proxy) GetMonitoringEventTypes() []string {
	return []string{monitoring.MetricProxyEstablishedConnections, monitoring.MetricProxyRunningRequests}
}

func (p *Proxy) SetObserver(mon monitoring.Observer) {
	p.mon = mon
}
