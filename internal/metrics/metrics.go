package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	DNSQueriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_queries_total",
			Help: "Total number of DNS queries processed, partitioned by protocol, status, and query type.",
		},
		[]string{"protocol", "status", "query_type"},
	)

	DNSQueryDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dns_query_duration_seconds",
			Help:    "DNS query processing latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12), // 0.5ms … ~1s
		},
		[]string{"protocol", "status"},
	)

	AnalyticsDroppedLogs = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "analytics_dropped_logs_total",
			Help: "Total number of analytics log entries dropped because of ClickHouse write failures.",
		},
	)
)

func init() {
	prometheus.MustRegister(DNSQueriesTotal, DNSQueryDurationSeconds, AnalyticsDroppedLogs)
}
