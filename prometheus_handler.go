package stats

import (
	"slices"

	"github.com/convoy-road-trips-app/stats/exporters/prometheus"
	"github.com/convoy-road-trips-app/stats/models"
)

// WithPrometheusHandler registers h, a Prometheus pull store, as a custom
// exporter (see WithExporter): the client folds every metric into h and a
// scrape of h (an http.Handler) renders them. A nil h makes NewClient fail with
// ErrInvalidConfig.
//
// When h.Buckets is nil, NewClient sets it to the client's histogram bucket
// lookup, so WithHistogramBucketsFor and the global OTLP HistogramBuckets
// choose the bounds of each histogram family, like the OTLP exporter does. The
// lookup is a snapshot taken at construction, after all options are applied, so
// option order does not matter. A Buckets function the caller set is never
// replaced.
//
// A Handler belongs to one client. Registering the same handler with several
// clients mixes their metrics into one store, and only the first client
// constructed gets to fill Buckets; that is the caller's concern. The client
// shuts the handler down on Close; the store stays readable afterwards.
func WithPrometheusHandler(h *prometheus.Handler) Option {
	if h == nil {
		return WithExporter(nil)
	}
	return WithExporter(&pullExporter{Handler: h})
}

// pullExporter registers a prometheus.Handler and binds its bucket lookup to
// the final configuration once the client is built.
type pullExporter struct {
	*prometheus.Handler
}

// bindBuckets fills the handler's Buckets from cfg when the caller left it nil.
// It copies what it needs, so later changes to cfg do not reach the handler.
func (p *pullExporter) bindBuckets(cfg *Config) {
	if p.Buckets != nil {
		return
	}
	byName := make(map[string][]float64, len(cfg.HistogramBucketsByName))
	for name, bounds := range cfg.HistogramBucketsByName {
		byName[name] = slices.Clone(bounds)
	}
	var global []float64
	if cfg.OTLP != nil {
		global = slices.Clone(cfg.OTLP.HistogramBuckets)
	}
	p.Buckets = func(name string) []float64 {
		return models.BucketsFor(byName, global, name)
	}
}

// bindPullHandlers resolves the bucket lookup of every handler registered with
// WithPrometheusHandler.
func bindPullHandlers(cfg *Config) {
	for _, e := range cfg.Exporters {
		if p, ok := e.(*pullExporter); ok {
			p.bindBuckets(cfg)
		}
	}
}
