package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"

	"github.com/sirupsen/logrus"
	"nhooyr.io/websocket"
)

type WSProxyHandler struct {
	connections     sync.Map //map[string]*Upstream
	log             *logrus.Logger
	messageMaxBytes int64
	client          *http.Client
}

func (h *WSProxyHandler) GetOrCreateUpstreamConnection(w http.ResponseWriter, r *http.Request) (*ProxiedConnection, error) {
	ctx := r.Context()
	if t, ok := h.connections.Load(middleware.GetConnectionID(r.Context())); !ok {

		conn, upstream, err := h.connect(ctx, r.Header)
		if err != nil {
			return nil, err
		}

		upst := ProxiedConnection{
			handler:              h,
			ctx:                  middleware.GetClientConnectionContext(ctx),
			initialRequestHeader: r.Header.Clone(),
			client:               r.RemoteAddr,
			clientConn:           middleware.GetClientConnection(ctx),
			upstream:             upstream,
			upstreamConn:         conn,
			requests:             sync.Map{},
			log:                  h.log,
			pause:                &sync.Mutex{},
		}
		h.connections.Store(middleware.GetConnectionID(r.Context()), &upst)
		go upst.poll()

		return &upst, nil
	} else {
		return t.(*ProxiedConnection), nil
	}
}

func (p *WSProxyHandler) connect(ctx context.Context, header http.Header) (*websocket.Conn, string, error) {
	proxyHeader := make(http.Header)
	middleware.CopyHeader(proxyHeader, header)
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

type RequestPayload struct {
	Request []byte
	Ready   chan []byte
}

type ProxiedConnection struct {
	handler              *WSProxyHandler
	ctx                  context.Context
	requests             sync.Map // map[int]chan []byte
	initialRequestHeader http.Header
	client               string
	clientConn           *websocket.Conn
	upstream             string
	upstreamConn         *websocket.Conn
	Unrecoverable        bool
	log                  *logrus.Logger
	pause                *sync.Mutex
}

func (pc *ProxiedConnection) UpstreamRead(r *http.Request) ([]byte, error) {
	ctx := r.Context()
	id := middleware.GetRPCRequestID(ctx)
	loaded, ok := pc.requests.Load(id)
	if !ok {
		return nil, fmt.Errorf("trying to read frame with id: %d that was not sent to upstream", id)
	}
	defer pc.requests.Delete(id)
	payload := loaded.(RequestPayload)
	for {
		select {
		case b := <-payload.Ready:
			return b, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pc.ctx.Done():
			return nil, pc.ctx.Err()
		}
	}
}

func (pc *ProxiedConnection) UpstreamWrite(r *http.Request) error {
	ctx := r.Context()
	id := middleware.GetRPCRequestID(ctx)
	// body already inspected and ReadAll must not fail
	b, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	ch := make(chan []byte, 1)
	// accept new requests only when connection not paused
	pc.pause.Lock()
	loaded, duplicate := pc.requests.LoadOrStore(id, RequestPayload{
		Request: b,
		Ready:   ch,
	})
	pc.pause.Unlock()
	if duplicate {
		p := loaded.(RequestPayload)
		pc.requests.Delete(id)
		close(p.Ready)
		return fmt.Errorf("found simultaneous requests with same frame Id: %d, buggy client?", id)
	}
	err := pc.upstreamConn.Write(ctx, websocket.MessageText, b)
	if err != nil && pc.Unrecoverable {
		return err
	}
	return nil
}

func (pc *ProxiedConnection) retryRunningRequests() bool {
	ok := true
	pc.requests.Range(func(key, value any) bool {
		rr := value.(RequestPayload)
		if err := pc.upstreamConn.Write(pc.ctx, websocket.MessageText, rr.Request); err != nil {
			ok = false
			return false
		}
		return true
	})
	return ok
}

func (pc *ProxiedConnection) handleUpstreamResponse(response []byte) error {
	var (
		err error
	)

	jsonRpc := middleware.GetJsonRPCMiddleware(pc.ctx)
	// respond to client
	parsed := jsonRpc.ParsePayload(response)
	if parsed == nil {
		return fmt.Errorf("upstream response is not valid json: %s", string(response))
	}
	f, ok := parsed.(dto.RPCFrame)
	if !ok {
		return fmt.Errorf("upstream response is not rpc frame: %s", string(response))
	}
	if f.Error != nil {
		middleware.AddLogField(pc.ctx, "error", fmt.Sprintf("%d: %s", f.Error.Code, f.Error.Message))
	}
	if rr, ok := pc.requests.Load(f.Id); ok {
		ch := rr.(RequestPayload)
		ch.Ready <- response
		return nil
	} else {
		// is upstream push: do direct response to the client
		pc.Unrecoverable = true
		middleware.LogSubscriptionDetails(pc.ctx, pc.client, pc.upstream, f)
		err = pc.clientConn.Write(pc.ctx, websocket.MessageText, response)
		return err
	}
}

func (pc *ProxiedConnection) poll() {
	cid := middleware.GetConnectionID(pc.ctx)
	defer func() {
		d := rand.Intn(500)
		time.Sleep(time.Duration(d) * time.Millisecond)
		pc.upstreamConn.Close(websocket.StatusNormalClosure, "")
		pc.handler.connections.Delete(cid)
		middleware.CancelConnection(pc.ctx, fmt.Errorf("connection must be closed"))
	}()

	var (
		err error
		b   []byte
	)
	for {
		select {
		case <-pc.ctx.Done():
			pc.log.WithError(err).
				WithField("upstream", pc.upstream).
				WithField("client", pc.client).
				Infof("%s parent connection closed", cid)
			return
		default:
			idle, cancel := context.WithTimeout(pc.ctx, defaultExecTimeout)
			_, b, err = pc.upstreamConn.Read(idle)
			cancel()
			if err == nil {
				err = pc.handleUpstreamResponse(b)
				if err != nil {
					pc.log.WithError(err).WithField("client", pc.client).WithField("upstream", pc.upstream).Infof("%s unable to handle upstream response", cid)
					return
				}
			} else {
				if !pc.IsRecoverable(err) {
					return
				}

				pc.upstreamConn.Close(websocket.StatusAbnormalClosure, "")
				if conn, upstream, err := pc.handler.connect(pc.ctx, pc.initialRequestHeader); err != nil {
					pc.log.WithError(err).
						WithField("upstream", pc.upstream).
						WithField("client", pc.client).
						Warnf("%s no live upstreams. client connection will be closed", cid)

					return
				} else {
					pc.upstreamConn = conn
					pc.upstream = upstream
				}
				pc.pause.Lock()
				if !pc.retryRunningRequests() {
					pc.pause.Unlock()
					pc.log.WithError(err).
						WithField("upstream", pc.upstream).
						WithField("client", pc.client).
						Warnf("%s no live upstreams. client connection will be closed", cid)

					return
				}
				pc.pause.Unlock()
				pc.log.WithError(err).
					WithField("upstream", pc.upstream).
					WithField("client", pc.client).
					Infof("%s upstream upstream changed", cid)
			}
		}
	}
}

func (pc *ProxiedConnection) IsRecoverable(err error) bool {
	cid := middleware.GetConnectionID(pc.ctx)
	if pc.ctx.Err() != nil || errors.Is(err, context.Canceled) {
		pc.log.WithError(err).
			WithField("upstream", pc.upstream).
			WithField("client", pc.client).
			Infof("%s client gone", cid)
		return false
	}
	if pc.Unrecoverable {
		pc.log.WithError(err).
			WithField("upstream", pc.upstream).
			WithField("client", pc.client).
			Warnf("%s unable to route connection to another upstream because it has subscriptions", cid)
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		pc.log.WithError(err).
			WithField("upstream", pc.upstream).
			WithField("client", pc.client).
			Infof("%s idle timeout client must be disconnected", cid)
		return false
	}
	return true
}
