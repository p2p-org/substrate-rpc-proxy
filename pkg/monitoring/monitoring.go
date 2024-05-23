package monitoring

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Observable interface {
	GetMonitoringEventTypes() []string
	SetObserver(Observer)
}

type Observer interface {
	ProcessEvent(string, float64, ...string)
	Reset(string)
	Register(Observable)
}

type PrometheusMetrics struct {
	ConsumerGap                 prometheus.Histogram
	ExtrinsicsCount             prometheus.Counter
	ResubmissionsCount          prometheus.Counter
	ExpiredCount                prometheus.Counter
	InvalidCount                prometheus.Counter
	AlreadyAddedCount           prometheus.Counter
	ConsumerMessagesInProgress  prometheus.Gauge
	ProxyRPCCalls               *prometheus.HistogramVec
	ProxyEstablishedConnections *prometheus.GaugeVec
	UpstreamEndpointsStatus     *prometheus.GaugeVec
	ProxyThortleBacklogSize     *prometheus.GaugeVec
	ProxyRunningRequests        *prometheus.GaugeVec
}

const (
	MetricExtrinsicsBlockGap           = "sub_extrinsics_block_gap"
	MetricExtrinsicsCount              = "sub_extrinsics_seen_count"
	MetricExtrinsicsResubmissionsCount = "sub_extrinsics_resubmitted_count"
	MetricExtrinsicsExpiredCount       = "sub_extrinsics_expired_count"
	MetricExtrinsicsAlreadyAddedCount  = "sub_extrinsics_already_added_count"
	MetricExtrinsicsInvalidCount       = "sub_extrinsics_rejected_by_node_count"
	MetricProxyRPCCalls                = "sub_proxy_rpc_calls"
	MetricProxyEstablishedConnections  = "sub_proxy_established_conn_count"
	MetricProxyRunningRequests         = "sub_proxy_running_requests_count"
	MetricProxyThortleBacklogSize      = "sub_proxy_throttle_available_slots"
	MerticConsumerMessagesInProgress   = "sub_consumer_messages_in_progress"
	MetricUpstreamEndpointsStatus      = "sub_upstream_endpoints"
)

func (p *PrometheusMetrics) Register(t Observable) {
	t.SetObserver(p)
	for _, name := range t.GetMonitoringEventTypes() {
		switch name {
		case MetricExtrinsicsBlockGap:
			p.ConsumerGap = promauto.NewHistogram(prometheus.HistogramOpts{
				Name:    MetricExtrinsicsBlockGap,
				Help:    "gap between first seen block and found in chain",
				Buckets: prometheus.ExponentialBuckets(4, 2, 7),
			})
		case MetricExtrinsicsCount:
			p.ExtrinsicsCount = promauto.NewCounter(prometheus.CounterOpts{
				Name: MetricExtrinsicsCount,
				Help: "extrinsics count by consumer",
			})
		case MetricExtrinsicsResubmissionsCount:
			p.ResubmissionsCount = promauto.NewCounter(prometheus.CounterOpts{
				Name: MetricExtrinsicsResubmissionsCount,
				Help: "extrinsics resubmissions count by consumer",
			})
		case MetricExtrinsicsExpiredCount:
			p.ExpiredCount = promauto.NewCounter(prometheus.CounterOpts{
				Name: MetricExtrinsicsExpiredCount,
				Help: "extrinsics expired count by consumer",
			})
		case MetricExtrinsicsInvalidCount:
			p.InvalidCount = promauto.NewCounter(prometheus.CounterOpts{
				Name: MetricExtrinsicsInvalidCount,
				Help: "extrinsics rejected by upstream",
			})
		case MetricExtrinsicsAlreadyAddedCount:
			p.AlreadyAddedCount = promauto.NewCounter(prometheus.CounterOpts{
				Name: MetricExtrinsicsAlreadyAddedCount,
				Help: "extrinsics already present in chain",
			})
		case MerticConsumerMessagesInProgress:
			p.ConsumerMessagesInProgress = promauto.NewGauge(prometheus.GaugeOpts{
				Name: MerticConsumerMessagesInProgress,
				Help: "number of messages currently active/processing by consumer",
			})
		case MetricProxyRPCCalls:
			p.ProxyRPCCalls = promauto.NewHistogramVec(prometheus.HistogramOpts{
				Name: MetricProxyRPCCalls,
				Help: "proxy request",
			}, []string{"method", "upstream_addr"})
		case MetricProxyEstablishedConnections:
			p.ProxyEstablishedConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: MetricProxyEstablishedConnections,
				Help: "proxy connections count",
			}, []string{"upstream_addr"})
		case MetricProxyRunningRequests:
			p.ProxyRunningRequests = promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: MetricProxyRunningRequests,
				Help: "number requests in flight",
			}, []string{"upstream_addr"})
		case MetricProxyThortleBacklogSize:
			p.ProxyThortleBacklogSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: MetricProxyThortleBacklogSize,
				Help: "available slots in backlog or limit",
			}, []string{"kind"})
		case MetricUpstreamEndpointsStatus:
			p.UpstreamEndpointsStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
				Name: MetricUpstreamEndpointsStatus,
				Help: "number of endpoints with gap and peers within treshold",
			}, []string{"upstream_addr", "status"})
		}
	}
}

func (p *PrometheusMetrics) ProcessEvent(name string, value float64, lvs ...string) {
	switch name {
	case MetricExtrinsicsBlockGap:
		if p.ConsumerGap != nil {
			p.ConsumerGap.Observe(float64(value))
		}
	case MetricExtrinsicsCount:
		if p.ExtrinsicsCount != nil {
			p.ExtrinsicsCount.Inc()
		}
	case MetricExtrinsicsResubmissionsCount:
		if p.ResubmissionsCount != nil {
			p.ResubmissionsCount.Inc()
		}
	case MetricExtrinsicsExpiredCount:
		if p.ExpiredCount != nil {
			p.ExpiredCount.Inc()
		}
	case MetricExtrinsicsInvalidCount:
		if p.InvalidCount != nil {
			p.InvalidCount.Inc()
		}
	case MetricExtrinsicsAlreadyAddedCount:
		if p.AlreadyAddedCount != nil {
			p.AlreadyAddedCount.Inc()
		}
	case MerticConsumerMessagesInProgress:
		if p.ConsumerMessagesInProgress != nil {
			p.ConsumerMessagesInProgress.Set(value)
		}
	case MetricProxyRPCCalls:
		if p.ProxyRPCCalls != nil {
			p.ProxyRPCCalls.WithLabelValues(lvs...).Observe(value)
		}
	case MetricProxyEstablishedConnections:
		if p.ProxyEstablishedConnections != nil {
			p.ProxyEstablishedConnections.WithLabelValues(lvs...).Set(value)
		}
	case MetricProxyRunningRequests:
		if p.ProxyRunningRequests != nil {
			p.ProxyRunningRequests.WithLabelValues(lvs...).Set(value)
		}
	case MetricUpstreamEndpointsStatus:
		if p.UpstreamEndpointsStatus != nil {
			p.UpstreamEndpointsStatus.Reset()
			for _, ep := range lvs {
				status := strings.Split(ep, "|")
				if len(status) == 2 {
					p.UpstreamEndpointsStatus.WithLabelValues(status[0], status[1]).Set(value)
				}
			}
		}
	case MetricProxyThortleBacklogSize:
		if p.ProxyThortleBacklogSize != nil {
			p.ProxyThortleBacklogSize.WithLabelValues(lvs...).Set(value)
		}
	}
}

func (p *PrometheusMetrics) Reset(name string) {
	switch name {
	case MetricProxyRunningRequests:
		if p.ProxyRunningRequests != nil {
			p.ProxyRunningRequests.Reset()
		}
	case MetricProxyEstablishedConnections:
		if p.ProxyEstablishedConnections != nil {
			p.ProxyEstablishedConnections.Reset()
		}
	}
}
