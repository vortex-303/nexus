package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	LLMCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_llm_calls_total",
		Help: "Total LLM API calls",
	}, []string{"model", "agent", "status"})

	LLMTokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_llm_tokens_total",
		Help: "Total LLM tokens",
	}, []string{"model", "direction"})

	LLMLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexus_llm_latency_seconds",
		Help:    "LLM call latency",
		Buckets: prometheus.ExponentialBuckets(0.5, 2, 8), // 0.5s to 64s
	}, []string{"model", "agent"})

	ToolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_tool_calls_total",
		Help: "Total tool calls",
	}, []string{"tool", "status"})

	ToolLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexus_tool_latency_seconds",
		Help:    "Tool execution latency",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 8),
	}, []string{"tool"})

	AgentExecutionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_agent_executions_total",
		Help: "Total agent executions",
	}, []string{"agent", "status"})

	WSConnectionsActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nexus_ws_connections_active",
		Help: "Active WebSocket connections",
	}, []string{"workspace"})

	MessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_messages_total",
		Help: "Total messages sent",
	}, []string{"workspace"})
)
