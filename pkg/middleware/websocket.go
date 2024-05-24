package middleware

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"nhooyr.io/websocket"
)

type WrapedWSWriter struct {
	w    http.ResponseWriter
	conn *websocket.Conn
	ctx  context.Context
}

func (w *WrapedWSWriter) Header() http.Header {
	return w.w.Header()
}

func (w *WrapedWSWriter) Write(b []byte) (int, error) {
	err := w.conn.Write(w.ctx, websocket.MessageText, b)
	return len(b), err
}

func (w *WrapedWSWriter) WriteHeader(statusCode int) {
	w.conn.Close(websocket.StatusBadGateway, "")
}

func WithContextValue(r *http.Request, key any, v any) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), key, v))
}

func IsWebsocket(r *http.Request) bool {
	if r.ProtoAtLeast(1, 1) && strings.EqualFold(r.Header.Get("Connection"), "Upgrade") && strings.EqualFold(r.Header.Get("Upgrade"), "Websocket") {
		return true
	}
	return false
}

func GetClientConnectionType(ctx context.Context) string {
	if t := ctx.Value(ConnectionTypeCtxKey); t != nil {
		return t.(string)
	}
	return ""
}

func GetConnectionID(ctx context.Context) string {
	if id := ctx.Value(ConnectionClientIDCtxKey); id != nil {
		return id.(string)
	}
	var buf [12]byte
	cryptorand.Read(buf[:])
	return fmt.Sprintf("%x", buf)
}

func AcceptWebsocket(next http.Handler) http.Handler {
	var reqid uint64
	var buf [12]byte
	cryptorand.Read(buf[:])
	prefix := fmt.Sprintf("%x", buf)

	fn := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&reqid, 1)
		// pass non-ws connections as is
		if !IsWebsocket(r) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, ConnectionClientIDCtxKey, fmt.Sprintf("%s-%06d", prefix, reqid))
			next.ServeHTTP(w, WithContextValue(r.WithContext(ctx), ConnectionTypeCtxKey, ConnectionTypeHTTPCtx))
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			render.Render(w, r, ErrorInternalServer(fmt.Errorf("server unable to accept connection. try later")))
			return
		}
		defer func() {
			d := rand.Intn(500)
			time.Sleep(time.Duration(d) * time.Millisecond)
			conn.Close(websocket.StatusNormalClosure, "")
		}()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		ctx = context.WithValue(ctx, ConnectionClientWSCancelCtxKey, cancel)
		// save and forward client connection for subscription direct cross-posting
		ctx = context.WithValue(ctx, ConnectionClientWSCtxKey, conn)
		ctx = context.WithValue(ctx, ConnectionTypeCtxKey, ConnectionTypeWebsocketCtx)
		ctx = context.WithValue(ctx, ConnectionClientIDCtxKey, fmt.Sprintf("%s-%06d", prefix, reqid))

		conn.SetReadLimit(15 * 1024 * 1024)
		ww := &WrapedWSWriter{
			w:    w,
			conn: conn,
			ctx:  ctx,
		}
		// save chi-router initial state
		rctx := ctx.Value(chi.RouteCtxKey).(*chi.Context)
		urlParams := rctx.URLParams
		routePatterns := rctx.RoutePatterns
		//go func() {
		//
		//}
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				// client connection is broken for some reason or idle
				return
			}
			// reset chi-router state for every new frame
			// to avoid memory leak in long living connections
			rctx.URLParams = urlParams
			rctx.RoutePatterns = routePatterns

			next.ServeHTTP(ww, WithNewBody(r.WithContext(ctx), b))

			if ctx.Err() != nil {
				return
			}
		}
	}
	return http.HandlerFunc(fn)
}

func CancelWebsocket(ctx context.Context, err error) {
	if cancel, ok := ctx.Value(ConnectionClientWSCancelCtxKey).(context.CancelFunc); ok {
		cancel()
	}
}
