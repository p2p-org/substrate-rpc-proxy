package middleware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"

	"github.com/go-chi/render"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
)

type RPCMiddleware interface {
	ParsePayload(payload []byte) interface{}
}

type JsonRPCMiddleware struct {
}

func (m *JsonRPCMiddleware) ParsePayload(payload []byte) interface{} {
	if len(payload) > 0 && strings.HasPrefix(string(payload), "[") {
		var batch dto.RPCBatch
		err := jsoniter.Unmarshal(payload, &batch)
		if err != nil {
			return nil
		}
		for i := 0; i < len(batch); i++ {
			frame, _ := jsoniter.Marshal(batch[i])
			batch[i].Raw = string(frame)
		}
		return batch
	} else {
		var frame dto.RPCFrame
		err := jsoniter.Unmarshal(payload, &frame)
		if err != nil {
			return nil
		}
		frame.Raw = string(payload)
		return frame
	}
}

func GetJsonRPCFrame(ctx context.Context) *dto.RPCFrame {
	if frame, ok := ctx.Value(RPCPayloadCtxKey).(*dto.RPCFrame); ok {
		return frame
	}
	return nil
}

func GetJsonRPCMiddleware(r *http.Request) *JsonRPCMiddleware {
	ctx := r.Context()
	if rpc := ctx.Value(RPCMiddlewareCtxKey); rpc != nil {
		if mw, ok := rpc.(*JsonRPCMiddleware); ok {
			return mw
		}
		return nil
	}
	return nil
}

func GetRPCRequestID(ctx context.Context) int {
	if id, ok := ctx.Value(RPCRequestID).(int); ok {
		return id
	}
	return 0
}

func GetRPCMethod(ctx context.Context) string {
	if m, ok := ctx.Value(RPCMethodCtxKey).(string); ok {
		return m
	}
	return ""
}

func AddLogField(ctx context.Context, k string, v string) {
	entryFields := ctx.Value(LogFieldsCtxKey)
	if entryFields == nil {
		return
	}

	entryFields.(logrus.Fields)[k] = v
}

func GetLogField(ctx context.Context, k string) string {
	entryFields := ctx.Value(LogFieldsCtxKey)
	if entryFields == nil {
		return ""
	}
	field, ok := entryFields.(logrus.Fields)[k]
	if ok {
		return field.(string)
	}
	return ""
}

func GetLogger(ctx context.Context) *logrus.Logger {
	return ctx.Value(LogLoggerCtxKey).(*logrus.Logger)
}

func logFieldsFromRequest(r *http.Request) logrus.Fields {
	logFields := make(logrus.Fields)
	ctx := r.Context()

	for k, v := range ctx.Value(LogFieldsCtxKey).(logrus.Fields) {
		logFields[k] = v
	}

	if r.Header.Get("x-forwarded-host") != "" {
		logFields["forwarded_host"] = r.Header.Get("x-forwarded-host")
	}
	logFields["client"] = r.RemoteAddr
	return logFields
}

func LogSubscriptionDetails(r *http.Request, method string, subscriptionId string) {
	ctx := r.Context()
	log := GetLogger(ctx)
	if log == nil {
		return
	}
	logFields := logFieldsFromRequest(r)
	logFields["direction"] = "server-push"
	logFields["sub_id"] = subscriptionId
	logFields["method"] = method

	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	log.WithFields(logFields).Debugf("%s %s", GetConnectionID(ctx), path)
}

func LogRequestDetails(r *http.Request) {
	ctx := r.Context()

	log := GetLogger(ctx)
	if log == nil {
		return
	}

	logFields := logFieldsFromRequest(r)

	if ctx.Value(LogWithParamsCtxKey).(bool) {
		if f := GetJsonRPCFrame(ctx); f != nil {
			logFields["method"] = f.Method
			logFields["id"] = f.Id
			logFields["params"] = fmt.Sprintf("%v", f.Params)
		}
	} else {
		logFields["method"] = GetRPCMethod(ctx)
		logFields["id"] = GetRPCRequestID(ctx)
	}

	startTime := ctx.Value(LogRequestStartTime).(time.Time)
	sec := time.Since(startTime).Seconds()
	logFields["duration"] = fmt.Sprintf("%fs", sec)
	logFields["direction"] = "request"

	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	log.WithFields(logFields).Debugf("%s %s", GetConnectionID(ctx), path)
}

func (m *JsonRPCMiddleware) NewFrameContext(ctx context.Context, f *dto.RPCFrame) context.Context {
	fctx := context.WithValue(ctx, RPCPayloadCtxKey, f)
	fctx = context.WithValue(fctx, RPCRequestID, f.Id)
	fctx = context.WithValue(fctx, RPCMethodCtxKey, f.Method)
	fctx = context.WithValue(fctx, RPCMiddlewareCtxKey, m)
	return fctx
}

func (m *JsonRPCMiddleware) requestsBatchFrom(r *http.Request, batch dto.RPCBatch) ([]*http.Request, error) {
	batchLen := len(batch)
	if batchLen > 50 {
		return nil, fmt.Errorf("batch can not exceed 50 items")
	}
	requests := make([]*http.Request, batchLen)
	for i := 0; i < batchLen; i++ {
		t, _ := jsoniter.Marshal(batch[i])
		fctx := m.NewFrameContext(r.Context(), &batch[i])
		rr := r.Clone(fctx)
		rr.Body = io.NopCloser(bytes.NewReader(t))
		rr.ContentLength = -1
		requests[i] = rr
	}
	return []*http.Request{}, nil
}

func writeBatchResponse(w http.ResponseWriter, writers []*InMemoryWriter) error {
	var (
		batch dto.RPCBatch
		err   error
	)
	for i := 0; i < len(writers); i++ {
		var frame dto.RPCFrame
		err = jsoniter.Unmarshal(writers[i].Buf, &frame)
		if err != nil {
			return err
		}
		batch[i] = frame
	}
	body, _ := jsoniter.Marshal(batch)
	CopyHeader(w.Header(), writers[0].Header())
	w.Header().Set("content-length", fmt.Sprintf("%d", len(body)))
	_, err = w.Write(body)
	return err
}

func JsonRPC(log *logrus.Logger, withParams bool) func(http.Handler) http.Handler {
	m := JsonRPCMiddleware{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entryFields := make(logrus.Fields)
			ctx := r.Context()
			ctx = context.WithValue(ctx, LogRequestStartTime, time.Now())
			ctx = context.WithValue(ctx, LogFieldsCtxKey, entryFields)
			ctx = context.WithValue(ctx, LogLoggerCtxKey, log)
			ctx = context.WithValue(ctx, LogWithParamsCtxKey, withParams)

			r = r.WithContext(ctx)

			// force correct content-type and method for websockets and bad clients
			r.Header.Set("content-type", "application/json")
			r.Header.Set("accept", "application/json")
			if r.Method == "GET" || r.Method == "OPTIONS" {
				r.Method = "POST"
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				render.Render(w, r, ErrorInternalServer(err))
				AddLogField(ctx, "error", err.Error())
				LogRequestDetails(r)
				return
			}
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(body))
			switch v := m.ParsePayload(body).(type) {
			case dto.RPCBatch:
				requests, err := m.requestsBatchFrom(r, v)
				if err != nil {
					render.Render(w, r, ErrorInvalidRequest(err))
					AddLogField(ctx, "error", err.Error())
					LogRequestDetails(r)
					return
				}
				writers := make([]*InMemoryWriter, len(requests))
				var wg sync.WaitGroup
				for i := 0; i < len(requests); i++ {
					writers[i] = NewInMemoryWriter(w)
					wg.Add(1)
					go func(writer http.ResponseWriter, request *http.Request) {
						next.ServeHTTP(writer, request)
						LogRequestDetails(r)
						defer wg.Done()
					}(writers[i], requests[i])
				}
				wg.Wait()
				err = writeBatchResponse(w, writers)
				if err != nil {
					render.Render(w, r, ErrorInternalServer(err))
					AddLogField(ctx, "error", err.Error())
					LogRequestDetails(r)
					return
				}
			case dto.RPCFrame:
				r = r.WithContext(m.NewFrameContext(ctx, &v))
				next.ServeHTTP(w, r)
				LogRequestDetails(r)
			default:
				next.ServeHTTP(w, r)
				LogRequestDetails(r)
			}
		})
	}
}
