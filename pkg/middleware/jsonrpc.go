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
	"github.com/sirupsen/logrus"

	jsoniter "github.com/json-iterator/go"
)

type RPCMiddleware interface {
	ParsePayload(payload []byte) interface{}
}

type JsonRPCMiddleware struct {
}

type JsonRPCMiddlewareOption func(http.Handler) http.Handler

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
	if f := ctx.Value(RPCPayloadCtxKey); f != nil {
		return f.(*dto.RPCFrame)
	}
	return nil
}

func GetJsonRPCMiddleware(ctx context.Context) *JsonRPCMiddleware {
	if rpc := ctx.Value(RPCMiddlewareCtxKey); rpc != nil {
		if mw, ok := rpc.(*JsonRPCMiddleware); ok {
			return mw
		}
		return nil
	}
	return nil
}

func GetRPCRequestID(ctx context.Context) int {
	if id := ctx.Value(RPCRequestID); id != nil {
		return id.(int)
	}
	return 0
}

func GetRPCMethod(ctx context.Context) string {
	if m := ctx.Value(RPCMethodCtxKey); m != nil {
		return m.(string)
	}
	return ""
}

func (m *JsonRPCMiddleware) NewFrameContext(ctx context.Context, f *dto.RPCFrame) context.Context {
	fctx := context.WithValue(ctx, RPCPayloadCtxKey, f)
	fctx = context.WithValue(fctx, LogFieldsCtxKey, make(logrus.Fields))
	fctx = context.WithValue(fctx, LogRequestStartTime, time.Now())
	fctx = context.WithValue(fctx, RPCRequestID, f.Id)
	fctx = context.WithValue(fctx, RPCMethodCtxKey, f.Method)
	fctx = context.WithValue(fctx, RPCMiddlewareCtxKey, m)
	return fctx
}

func (m *JsonRPCMiddleware) splitBatchToSubrequests(r *http.Request, batch dto.RPCBatch) ([]*http.Request, error) {
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
	return requests, nil
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
		batch = append(batch, frame)
	}
	body, _ := jsoniter.Marshal(batch)
	CopyHeader(w.Header(), writers[0].Header())
	w.Header().Set("content-length", fmt.Sprintf("%d", len(body)))
	_, err = w.Write(body)
	return err
}

func JsonRPC(opts ...JsonRPCMiddlewareOption) func(http.Handler) http.Handler {
	m := JsonRPCMiddleware{}

	return func(next http.Handler) http.Handler {
		for i := 0; i < len(opts); i++ {
			next = opts[i](next)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// force correct content-type and method for websockets and bad clients
			r.Header.Set("content-type", "application/json")
			r.Header.Set("accept", "application/json")
			if r.Method == "GET" || r.Method == "OPTIONS" {
				r.Method = "POST"
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				CancelConnection(r.Context(), err)
				return
			}
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(body))
			switch v := m.ParsePayload(body).(type) {
			case dto.RPCBatch:
				requests, err := m.splitBatchToSubrequests(r, v)
				if err != nil {
					CancelConnection(r.Context(), err)
					return
				}
				writers := make([]*InMemoryWriter, len(requests))
				var wg sync.WaitGroup
				for i := 0; i < len(requests); i++ {
					writers[i] = NewInMemoryWriter(w)
					wg.Add(1)
					go func(writer http.ResponseWriter, request *http.Request) {
						next.ServeHTTP(writer, request)
						LogRequestDetails(request)
						defer wg.Done()
					}(writers[i], requests[i])
				}
				wg.Wait()
				err = writeBatchResponse(w, writers)
				if err != nil {
					CancelConnection(r.Context(), err)
					return
				}
			case dto.RPCFrame:
				r = r.WithContext(m.NewFrameContext(r.Context(), &v))
				next.ServeHTTP(w, r)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
