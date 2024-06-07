package middleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/go-chi/render"
)

type contextKey string

const (
	contextKeyClientAddr         = contextKey("ClientAddr")
	ContextKeyUstreamProvider    = contextKey("UpstreamProvider")
	ContextKeyUstreamAddr        = contextKey("UpstreamAddr")
	LogFieldsCtxKey              = contextKey("LogFields")
	LogWithParamsCtxKey          = contextKey("LogWithParams")
	LogRequestStartTime          = contextKey("LogRequestStartTime")
	LogLoggerCtxKey              = contextKey("LogLogger")
	contextKeyResponseText       = contextKey("ResponseText")
	RPCPayloadCtxKey             = contextKey("RPCPayload")
	RPCMethodCtxKey              = contextKey("RPCMethod")
	RPCRequestID                 = contextKey("RPCRequestID")
	RPCMiddlewareCtxKey          = contextKey("RPCMiddleware")
	ConnectionTypeCtxKey         = contextKey("ConnectionType")
	ConnectionClientWSCtxKey     = contextKey("ConnectionClientWS")
	ConnectionClientIDCtxKey     = contextKey("ConnectionClientID")
	ConnectionClientCancelCtxKey = contextKey("ConnectionClientCancel")
)

const (
	ConnectionTypeWebsocketCtx = "websocket"
	ConnectionTypeHTTPCtx      = "http"
	RPCPayloadTypeBatchCtx     = "batch"
	RPCPayloadTypeFrameCtx     = "frame"
)

type ErrorResponse struct {
	Err            error  `json:"-"`
	HTTPStatusCode int    `json:"-"`
	StatusText     string `json:"status"`
	ErrorText      string `json:"error,omitempty"`
}

func (e *ErrorResponse) Render(w http.ResponseWriter, r *http.Request) error {
	render.Status(r, e.HTTPStatusCode)
	return nil
}

func ErrorInvalidRequest(err error) *ErrorResponse {
	return &ErrorResponse{
		Err:            err,
		HTTPStatusCode: http.StatusBadRequest,
		StatusText:     "Invalid request",
		ErrorText:      err.Error(),
	}
}

func ErrorInternalServer(err error) *ErrorResponse {
	return &ErrorResponse{
		Err:            err,
		HTTPStatusCode: http.StatusInternalServerError,
		StatusText:     "Internal server error",
		ErrorText:      err.Error(),
	}
}

func ErrorBadGateway(err error) *ErrorResponse {
	return &ErrorResponse{
		Err:            err,
		HTTPStatusCode: http.StatusBadGateway,
		StatusText:     "Bad gateway",
		ErrorText:      err.Error(),
	}
}

func WithNewBody(r *http.Request, b []byte) *http.Request {
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader([]byte(b)))
	r.Body = io.NopCloser(bytes.NewReader([]byte(b)))
	//r2.ContentLength = -1
	return r2
}

func CopyHeader(dst, src http.Header) {
	var skipHeaders = []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
	}
	for k, vv := range src {
		skip := false
		for _, name := range skipHeaders {
			if name == k {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		for _, v := range vv {
			dst.Set(k, v)
		}
	}
}
