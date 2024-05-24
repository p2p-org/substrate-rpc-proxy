package middleware

import (
	"context"
	"net/http"

	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"
)

func EndpointProvider(prov endpoint.Provider) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var assignedUpstream string
			ctx := r.Context()
			if IsWebsocket(r) {
				assignedUpstream = prov.GetAliveEndpoint(ConnectionTypeWebsocketCtx, 0)
			} else {
				assignedUpstream = prov.GetAliveEndpoint(ConnectionTypeHTTPCtx, 0)
			}
			AddLogField(ctx, "upstream", assignedUpstream)
			ctx = context.WithValue(ctx, ContextKeyUstreamAddr, assignedUpstream)
			ctx = context.WithValue(ctx, ContextKeyUstreamProvider, prov)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetAssignedUpstream(ctx context.Context) string {
	if ct := ctx.Value(ContextKeyUstreamAddr); ct != nil {
		return ct.(string)
	}
	return ""
}

func GetEndpointProvider(ctx context.Context) endpoint.Provider {
	if prov := ctx.Value(ContextKeyUstreamProvider); prov != nil {
		return prov.(endpoint.Provider)
	}
	return nil
}
