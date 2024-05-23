package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"

	jsoniter "github.com/json-iterator/go"
)

func DenyRPCMethods(methods []string) func(http.Handler) http.Handler {

	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if method, ok := ctx.Value(RPCMethodCtxKey).(string); ok {
				for _, m := range methods {
					if strings.EqualFold(m, method) {
						if id, ok := ctx.Value(RPCRequestID).(int); ok {
							resp, _ := jsoniter.Marshal(dto.RPCFrame{
								RPC: "2.0",
								Id:  id,
								Error: &dto.RPCError{
									Code:    -32601,
									Message: "RPC call is unsafe to be called externally",
								},
							})
							if !IsWebsocket(r) {
								w.Header().Set("content-length", fmt.Sprintf("%d", len(resp)))
							}
							_, err := w.Write(resp)
							if err != nil {
								AddLogField(ctx, "error", err.Error())
							}
							AddLogField(ctx, "denied", "yes")
							return
						}
					}
				}
			}

			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}

}
