package http

import (
	stdhttp "net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Phase 9 T085 — strictly DECLARES Prometheus metric objects and registers
// them with a Registry. T107 wires the .Inc() / .Observe() calls at the
// Apply* exit points and the SSE hub drop path. The split is mechanically
// precise (per the L1 remediation in /speckit-analyze pass 3): no value
// changes happen in this file.
//
// Series listed in contracts/http-api.md /metrics.

var (
	// Registry is the kisschess Prometheus registry. Exposed at GET /metrics.
	// Independent of the default global registry so we don't accidentally
	// expose process metrics we haven't reviewed.
	Registry = prometheus.NewRegistry()

	// MetricGamesActive is the current count of active rooms.
	MetricGamesActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kiss_games_active",
		Help: "Number of authoritative GameRooms currently held in process memory.",
	})

	// MetricSSESubscribers is the total subscriber count across all rooms.
	MetricSSESubscribers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kiss_sse_subscribers",
		Help: "Total live SSE subscribers across all games.",
	})

	// MetricMovesAccepted breaks down move-outcomes for fine-grained debugging.
	// Retained alongside MetricActionsAccepted for move-only diagnosis.
	MetricMovesAccepted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kiss_moves_accepted_total",
			Help: "Move-outcome breakdown (ok|stale|illegal|not_turn|flagged|bad_syntax).",
		},
		[]string{"outcome"},
	)

	// MetricActionsAccepted — the canonical accepted-action counter
	// covering every state-changing action (moves + resigns + draw offers
	// + draw accepts). The action-kind label is the discriminator.
	MetricActionsAccepted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kiss_actions_accepted_total",
			Help: "Every accepted state-changing action, labelled by kind.",
		},
		[]string{"kind"},
	)

	// MetricSSEDropped is the cumulative slow-reader drop count.
	MetricSSEDropped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kiss_sse_dropped_total",
			Help: "Subscribers dropped, labelled by reason.",
		},
		[]string{"reason"},
	)

	// MetricPublishQueueDepth observes the per-subscriber buffer fill at
	// publish time. Sampled, not exhaustive.
	MetricPublishQueueDepth = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "kiss_publish_queue_depth",
		Help:    "Per-subscriber Send-channel fill observed at hub publish time.",
		Buckets: []float64{0, 1, 2, 4, 8, 12, 14, 15, 16},
	})

	// MetricApplyActionDuration measures the lock-window for each Apply*
	// path. Buckets sized for the typical 10µs-1ms range.
	MetricApplyActionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kiss_apply_action_duration_seconds",
			Help:    "Wall-clock time inside the room lock for state-changing actions.",
			Buckets: []float64{0.00001, 0.00005, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
		},
		[]string{"kind"},
	)
)

// init registers every metric with the kisschess registry. No call-site
// wiring here — that's T107's job in internal/chess.
func init() {
	Registry.MustRegister(
		MetricGamesActive,
		MetricSSESubscribers,
		MetricMovesAccepted,
		MetricActionsAccepted,
		MetricSSEDropped,
		MetricPublishQueueDepth,
		MetricApplyActionDuration,
	)
}

// MetricsHandler returns the /metrics HTTP handler bound to our Registry.
// Replaces the placeholder from Phase 2 (cmd/server/main.go wires it).
func MetricsHandler() stdhttp.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: false,
	})
}
