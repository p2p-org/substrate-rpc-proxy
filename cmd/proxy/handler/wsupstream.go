package handler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"

	"github.com/sirupsen/logrus"
	"nhooyr.io/websocket"
)

type Upstream struct {
	handler       *RPCHandler
	proxy         *Proxy
	parentCtx     context.Context
	conn          *websocket.Conn
	requests      sync.Map // map[int]chan []byte
	client        string
	server        string
	Unrecoverable bool
	log           *logrus.Logger
	pause         *sync.Mutex
}

func (ws *Upstream) IsRecoverable(err error) bool {
	cid := middleware.GetConnectionID(ws.parentCtx)
	if ws.parentCtx.Err() != nil || errors.Is(err, context.Canceled) {
		ws.log.WithError(err).
			WithField("upstream", ws.server).
			WithField("client", ws.client).
			Infof("%s client gone", cid)
		return false
	}
	if ws.Unrecoverable {
		ws.log.WithError(err).
			WithField("upstream", ws.server).
			WithField("client", ws.client).
			Infof("%s client has subscriptions or operating raw bytes must be disconnected", cid)
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		ws.log.WithError(err).
			WithField("upstream", ws.server).
			WithField("client", ws.client).
			Infof("%s idle timeout client must be disconnected", cid)
		return false
	}
	return true
}

func (ws *Upstream) WriteAndGetResponse(ctx context.Context, id int, req []byte) ([]byte, error) {
	// Limit single upstream request execution
	ctx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
	defer cancel()
	if ch, ok := ws.requests.Load(id); ok {
		ws.log.Warn("id duplicate: faulty client or missed write")
		ch := ch.(RRPayloads)
		close(ch.ResposeCh)
	}
	ch := make(chan []byte, 1)
	defer close(ch)
	ws.pause.Lock()
	ws.requests.Store(id, RRPayloads{
		Request:   req,
		ResposeCh: ch,
	})

	defer ws.requests.Delete(id)
	err := ws.conn.Write(ctx, websocket.MessageText, req)
	ws.pause.Unlock()
	if err != nil && ws.Unrecoverable {
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (ws *Upstream) retryRunningRequests() bool {
	ok := true
	ws.requests.Range(func(key, value any) bool {
		rr := value.(RRPayloads)
		if err := ws.conn.Write(ws.parentCtx, websocket.MessageText, rr.Request); err != nil {
			ok = false
			return false
		}
		return true
	})
	return ok
}

func (ws *Upstream) handleUpstreamResponse(w http.ResponseWriter, r *http.Request, response []byte) error {
	var (
		err error
	)

	jsonRpc := middleware.GetJsonRPCMiddleware(r)
	if jsonRpc == nil {
		ws.log.WithField("client", ws.client).WithField("upstream", ws.server).Info("RPC middleware not defined request will be processed as raw bytes")
		ws.Unrecoverable = true
	}
	sent := false
	// response to client handler
	if jsonRpc != nil {
		f, ok := jsonRpc.ParsePayload(response).(dto.RPCFrame)
		// is raw bytes
		if !ok {
			ws.Unrecoverable = true
		} else {
			if rr, ok := ws.requests.Load(f.Id); ok {
				ch := rr.(RRPayloads)
				ch.ResposeCh <- response
				sent = true
			} else {
				// is server push
				if f.Id == 0 {
					ws.Unrecoverable = true
					middleware.LogSubscriptionDetails(r, f.Method, dto.MustMap(f.Params).MustString("subscription"))
				}
			}
		}
	}
	// direct response to the client
	if !sent || jsonRpc == nil {
		if _, err = w.Write(response); err != nil {
			return err
		}
	}
	return nil
}

func (ws *Upstream) poll(w http.ResponseWriter, r *http.Request) {
	cid := middleware.GetConnectionID(ws.parentCtx)
	defer func() {
		d := rand.Intn(500)
		time.Sleep(time.Duration(d) * time.Millisecond)
		ws.conn.Close(websocket.StatusNormalClosure, "")
	}()
	defer ws.handler.wsUpstreams.Delete(cid)
	defer middleware.CancelWebsocket(ws.parentCtx, fmt.Errorf("connection must be closed"))
	var (
		err error
		b   []byte
	)
	for {
		select {
		case <-ws.parentCtx.Done():
			return
		default:
			idle, cancel := context.WithTimeout(ws.parentCtx, defaultExecTimeout)
			_, b, err = ws.conn.Read(idle)
			cancel()
			if err != nil {
				if !ws.IsRecoverable(err) {
					return
				}
				ws.pause.Lock()
				ws.conn.Close(websocket.StatusAbnormalClosure, "")
				if conn, server, err := ws.proxy.Connect(r); err != nil {
					ws.log.WithError(err).
						WithField("upstream", ws.server).
						WithField("client", ws.client).
						Warnf("%s no live upstreams. client connection will be closed", cid)

					ws.pause.Unlock()
					return
				} else {
					ws.conn = conn
					ws.server = server
				}
				if !ws.retryRunningRequests() {
					ws.log.WithError(err).
						WithField("upstream", ws.server).
						WithField("client", ws.client).
						Warnf("%s no live upstreams. client connection will be closed", cid)

					ws.pause.Unlock()
					return
				}
				ws.pause.Unlock()
				ws.log.WithError(err).
					WithField("upstream", ws.server).
					WithField("client", ws.client).
					Infof("%s upstream server changed", cid)
			} else {
				err = ws.handleUpstreamResponse(w, r, b)
				if err != nil {
					ws.log.WithError(err).WithField("client", ws.client).WithField("upstream", ws.server).Infof("%s unable to write response to client", cid)
					return
				}
			}
		}
	}
}
