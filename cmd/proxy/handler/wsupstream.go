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
			Warnf("%s upstream error: must close client connection because it has subscriptions", cid)
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
		ch := ch.(RunningRequestPayloads)
		close(ch.ResposeCh)
	}
	ch := make(chan []byte, 1)
	defer close(ch)
	// accept new requests only when connection not paused
	ws.pause.Lock()
	ws.requests.Store(id, RunningRequestPayloads{
		Request:   req,
		ResposeCh: ch,
	})
	ws.pause.Unlock()
	defer ws.requests.Delete(id)
	err := ws.conn.Write(ctx, websocket.MessageText, req)
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
		rr := value.(RunningRequestPayloads)
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
		_, err = w.Write(response)
		return err
	}
	// respond to client
	parsed := jsonRpc.ParsePayload(response)
	if parsed == nil {
		return fmt.Errorf("nil response from upstream")
	}
	f, ok := parsed.(dto.RPCFrame)
	if !ok {
		return fmt.Errorf("upstream sent some bytes that does not appear to be rpc frame")
	}

	if rr, ok := ws.requests.Load(f.Id); ok {
		ch := rr.(RunningRequestPayloads)
		ch.ResposeCh <- response
		return nil
	} else {
		// is server push
		if f.Id == 0 {
			ws.Unrecoverable = true
			middleware.LogSubscriptionDetails(r, f.Method, dto.MustMap(f.Params).MustString("subscription"))
		}
		_, err = w.Write(response)
		return err
	}
}

func (ws *Upstream) poll(w http.ResponseWriter, r *http.Request) {
	cid := middleware.GetConnectionID(ws.parentCtx)
	defer func() {
		d := rand.Intn(500)
		time.Sleep(time.Duration(d) * time.Millisecond)
		ws.conn.Close(websocket.StatusNormalClosure, "")
	}()
	defer ws.handler.wsUpstreams.Delete(cid)
	defer middleware.CancelConnection(w, r, fmt.Errorf("connection must be closed"))
	var (
		err error
		b   []byte
	)
	for {
		select {
		case <-ws.parentCtx.Done():
			ws.log.WithError(err).
				WithField("upstream", ws.server).
				WithField("client", ws.client).
				Infof("%s parent connection closed", cid)
			return
		default:
			idle, cancel := context.WithTimeout(ws.parentCtx, defaultExecTimeout)
			_, b, err = ws.conn.Read(idle)
			cancel()
			if err != nil {
				if !ws.IsRecoverable(err) {
					return
				}

				ws.conn.Close(websocket.StatusAbnormalClosure, "")
				if conn, server, err := ws.proxy.Connect(r); err != nil {
					ws.log.WithError(err).
						WithField("upstream", ws.server).
						WithField("client", ws.client).
						Warnf("%s no live upstreams. client connection will be closed", cid)

					return
				} else {
					ws.conn = conn
					ws.server = server
				}
				ws.pause.Lock()
				if !ws.retryRunningRequests() {
					ws.pause.Unlock()
					ws.log.WithError(err).
						WithField("upstream", ws.server).
						WithField("client", ws.client).
						Warnf("%s no live upstreams. client connection will be closed", cid)

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
