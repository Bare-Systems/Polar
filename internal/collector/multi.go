// multi.go provides a MultiCollector that aggregates readings from multiple
// collector.Service implementations (B-3).
package collector

import "polar/pkg/contracts"

// MultiCollector fans readings out from a list of subordinate collectors.
type MultiCollector struct {
	collectors []Service
}

// NewMultiCollector returns a collector that merges readings from all provided
// collectors. nil entries are silently skipped so callers can pass optional
// collectors without pre-filtering.
func NewMultiCollector(collectors ...Service) *MultiCollector {
	active := make([]Service, 0, len(collectors))
	for _, c := range collectors {
		if c != nil {
			active = append(active, c)
		}
	}
	return &MultiCollector{collectors: active}
}

// Collect calls every subordinate collector and merges their results.
func (m *MultiCollector) Collect() []contracts.Reading {
	var out []contracts.Reading
	for _, c := range m.collectors {
		out = append(out, c.Collect()...)
	}
	return out
}

// Metrics returns the union of all supported metric names. Only implemented
// when every sub-collector exposes a Metrics() method.
func (m *MultiCollector) Metrics() []string {
	seen := make(map[string]struct{})
	for _, c := range m.collectors {
		type metricsProvider interface {
			Metrics() []string
		}
		if mp, ok := c.(metricsProvider); ok {
			for _, name := range mp.Metrics() {
				seen[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out
}
