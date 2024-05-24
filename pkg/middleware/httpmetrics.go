package middleware

import (
	"net/http"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"
)

type HttpMetrics struct {
	mon monitoring.Observer
}

func (m *HttpMetrics) GetMonitoringEventTypes() []string {
	return []string{
		monitoring.MetricProxyRPCCalls,
	}
}

func (m *HttpMetrics) SetObserver(mon monitoring.Observer) {
	m.mon = mon
}

func (m *HttpMetrics) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			ctx := r.Context()
			duration := -1.0

			if t := ctx.Value(LogRequestStartTime); t != nil {
				startTime := t.(time.Time)
				duration = time.Since(startTime).Seconds()
			}
			upstream := GetLogField(ctx, "upstream")
			m.mon.ProcessEvent(monitoring.MetricProxyRPCCalls, duration, GetRPCMethod(ctx), endpoint.FormatUpstream(upstream))
		})
	}
}
