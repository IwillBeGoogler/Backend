package diagramhub

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Realtime metrics, exported on the same /metrics as the HTTP ones. No labels
// carry file or client ids: cardinality stays fixed.
var (
	yjsIn = promauto.NewCounter(prometheus.CounterOpts{
		Name: "iwillbegoogler_yjs_updates_in_total",
		Help: "Yjs updates from editors that carried something new.",
	})
	yjsOut = promauto.NewCounter(prometheus.CounterOpts{
		Name: "iwillbegoogler_yjs_updates_out_total",
		Help: "Yjs updates relayed to editors on yjs/down.",
	})
	yjsFlush = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "iwillbegoogler_yjs_batch_write_seconds",
		Help:    "Latency of writing one batch of Yjs updates to the database.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	})
	yjsFlushFail = promauto.NewCounter(prometheus.CounterOpts{
		Name: "iwillbegoogler_yjs_batch_write_failures_total",
		Help: "Yjs batch writes that failed.",
	})
	yjsRooms = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "iwillbegoogler_yjs_open_rooms",
		Help: "Yjs rooms held in memory by this instance.",
	})
	yjsCompactDone = promauto.NewCounter(prometheus.CounterOpts{
		Name: "iwillbegoogler_yjs_compactions_total",
		Help: "Yjs compactions stored.",
	})
	yjsCompactRefused = promauto.NewCounter(prometheus.CounterOpts{
		Name: "iwillbegoogler_yjs_compactions_refused_total",
		Help: "Yjs compactions refused because the snapshot did not cover every stored update.",
	})
	yjsStored = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "iwillbegoogler_yjs_stored_updates",
		Help:    "Stored updates a file holds beyond its snapshot, observed at each batch write.",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500},
	})
)
