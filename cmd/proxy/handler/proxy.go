package handler

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"

	"github.com/go-chi/render"
	"github.com/sirupsen/logrus"
	"nhooyr.io/websocket"
)

const (
	defaultExecTimeout = 60 * time.Minute
)

type RunningRequestPayloads struct {
	Request   []byte
	ResposeCh chan []byte
}

type Proxy struct {
	client          *http.Client
	log             *logrus.Logger
	mon             monitoring.Observer
	messageMaxBytes int64
	allowedOrigins  []string
}

type RPCHandler struct {
	proxy       *Proxy
	wsUpstreams sync.Map //map[string]*Upstream
	log         *logrus.Logger
}

func NewProxy(l *logrus.Logger) *Proxy {

	return &Proxy{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     defaultExecTimeout,
				Dial: (&net.Dialer{
					Timeout: 60 * time.Second,
				}).Dial,
			},
		},
		log:             l,
		messageMaxBytes: 15 * 1024 * 1024,
		allowedOrigins:  []string{"*"},
	}
}

func (p *Proxy) Connect(r *http.Request) (*websocket.Conn, string, error) {
	ctx := r.Context()
	proxyHeader := make(http.Header)
	middleware.CopyHeader(proxyHeader, r.Header)
	prov := middleware.GetEndpointProvider(ctx)
	tries := []string{}
	for i := 0; i < 3; i++ {
		url := prov.GetAliveEndpoint("websocket", 1)

		conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: proxyHeader,
			HTTPClient: p.client,
		})
		if err != nil {
			time.Sleep(5 * time.Second)
			tries = append(tries, url)
			continue
		}
		conn.SetReadLimit(p.messageMaxBytes)
		return conn, url, nil
	}
	return nil, "", fmt.Errorf("unable to create upstream connection tried: %s", strings.Join(tries, ","))
}

func (p *Proxy) RPCHandler() *RPCHandler {
	h := RPCHandler{
		proxy: p,
		log:   p.log,
	}
	go h.emitMetrics()
	return &h
}

func (h *RPCHandler) GetOrCreateWSUpstreamConnection(w http.ResponseWriter, r *http.Request) (*Upstream, error) {
	if t, ok := h.wsUpstreams.Load(middleware.GetConnectionID(r.Context())); !ok {

		conn, server, err := h.proxy.Connect(r)
		if err != nil {
			return nil, err
		}

		upst := Upstream{
			handler:   h,
			proxy:     h.proxy,
			parentCtx: r.Context(),
			conn:      conn,
			requests:  sync.Map{},
			log:       h.log,
			pause:     &sync.Mutex{},
			server:    server,
			client:    r.RemoteAddr,
		}
		h.wsUpstreams.Store(middleware.GetConnectionID(r.Context()), &upst)
		go upst.poll(w, r)

		return &upst, nil
	} else {
		return t.(*Upstream), nil
	}
}

func (h *RPCHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b []byte
	ctx := r.Context()

	switch middleware.GetClientConnectionType(ctx) {
	case middleware.ConnectionTypeWebsocketCtx:
		upst, err := h.GetOrCreateWSUpstreamConnection(w, r)
		middleware.AddLogField(ctx, "upstream", upst.server)
		if err != nil {
			h.log.WithError(err).WithField("client", r.RemoteAddr).Warnf("connection with upstream failed")
			middleware.CancelConnection(w, r, err)
			return
		}

		defer r.Body.Close()
		// should not fail: body was already read and set by RPC middleware
		b, _ = io.ReadAll(r.Body)
		var resp []byte
		if middleware.GetRPCRequestID(ctx) != 0 {
			if resp, err = upst.WriteAndGetResponse(ctx, middleware.GetRPCRequestID(ctx), b); err != nil {
				h.log.WithError(err).WithField("client", upst.client).WithField("upstream", upst.server).Warnf("failed to pass request and read response from upstream")
				middleware.CancelConnection(w, r, err)
				return
			}
			// send response back to client
			if _, err = w.Write(resp); err != nil {
				h.log.WithError(err).WithField("client", upst.client).WithField("upstream", upst.server).Warnf("unable to write response to client")
				middleware.CancelConnection(w, r, err)
				return
			}
		} else {
			// RPCRequestID unknown pass request as-is and let Upstream.poll copy all responses to client
			if err := upst.conn.Write(ctx, websocket.MessageText, b); err != nil {
				h.log.WithError(err).WithField("client", upst.client).WithField("upstream", upst.server).Warnf("unable to write response to client")
				middleware.CancelConnection(w, r, err)
				return
			}
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
		upstreamResp, err := h.proxy.client.Do(r)
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
		if _, err = w.Write(b); err != nil {
			return
		}
	}
}

func (h *RPCHandler) emitMetrics() {
	ticker := time.NewTicker(15 * time.Second)
	for range ticker.C {
		connectionsCount := make(map[string]int)
		requestsCount := make(map[string]int)
		h.wsUpstreams.Range(func(key, value any) bool {
			upst := value.(*Upstream)

			host := endpoint.FormatUpstream(upst.server)
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
		h.proxy.mon.Reset(monitoring.MetricProxyEstablishedConnections)
		for host, c := range connectionsCount {
			h.proxy.mon.ProcessEvent(monitoring.MetricProxyEstablishedConnections, float64(c), host)
		}
		h.proxy.mon.Reset(monitoring.MetricProxyRunningRequests)
		for host, c := range requestsCount {
			h.proxy.mon.ProcessEvent(monitoring.MetricProxyRunningRequests, float64(c), host)
		}
	}
}

func (p *Proxy) GetMonitoringEventTypes() []string {
	return []string{monitoring.MetricProxyEstablishedConnections, monitoring.MetricProxyRunningRequests}
}

func (p *Proxy) SetObserver(mon monitoring.Observer) {
	p.mon = mon
}
