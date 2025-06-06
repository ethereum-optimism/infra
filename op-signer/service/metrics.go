package service

import "github.com/prometheus/client_golang/prometheus"

var (
	MetricSignTransactionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "signer_signtransaction_total",
			Help: ""},
		[]string{"client", "status", "error"},
	)

	MetricSignBlockPayloadTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "signer_signblockpayload_total",
			Help: ""},
		[]string{"client", "status", "error"},
	)
)
