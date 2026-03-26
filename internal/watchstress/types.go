package watchstress

import (
	"sync"
	"time"
)

// Mode controls which watch stress modes to run.
type Mode string

const (
	ModeStandalone Mode = "standalone"
	ModeCombined   Mode = "combined"
	ModeBoth       Mode = "both"
)

// Config holds watch stress test configuration.
type Config struct {
	Connections   int
	Duration      time.Duration
	ResourceTypes []string
	Mode          Mode
	Namespace     string // namespace prefix for namespaced resources
	SpreadCount   int    // number of spread namespaces to watch
}

// Metrics collects watch performance data.
type Metrics struct {
	mu sync.Mutex

	TotalEvents            int64
	EventsPerSecond        float64
	ConnectLatencies       []time.Duration
	EventDeliveryLatencies []time.Duration
	Reconnects             int64
	Errors                 int64
	StartTime              time.Time
	EndTime                time.Time
}

// AddConnectLatency records a watch connection establishment latency.
func (m *Metrics) AddConnectLatency(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ConnectLatencies = append(m.ConnectLatencies, d)
}

// AddDeliveryLatency records event delivery latency (create time → watch receive).
func (m *Metrics) AddDeliveryLatency(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EventDeliveryLatencies = append(m.EventDeliveryLatencies, d)
}

// Summary computes derived metrics.
func (m *Metrics) Summary() MetricsSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	duration := m.EndTime.Sub(m.StartTime).Seconds()
	if duration <= 0 {
		duration = 1
	}

	s := MetricsSummary{
		TotalEvents:     m.TotalEvents,
		EventsPerSecond: float64(m.TotalEvents) / duration,
		Reconnects:      m.Reconnects,
		Errors:          m.Errors,
		Duration:        m.EndTime.Sub(m.StartTime),
	}

	if len(m.ConnectLatencies) > 0 {
		s.AvgConnectLatency = avg(m.ConnectLatencies)
		s.MaxConnectLatency = max(m.ConnectLatencies)
	}
	if len(m.EventDeliveryLatencies) > 0 {
		s.AvgDeliveryLatency = avg(m.EventDeliveryLatencies)
		s.MaxDeliveryLatency = max(m.EventDeliveryLatencies)
		s.P99DeliveryLatency = percentile(m.EventDeliveryLatencies, 0.99)
	}

	return s
}

// MetricsSummary is the computed summary of watch metrics.
type MetricsSummary struct {
	TotalEvents        int64
	EventsPerSecond    float64
	Reconnects         int64
	Errors             int64
	Duration           time.Duration
	AvgConnectLatency  time.Duration
	MaxConnectLatency  time.Duration
	AvgDeliveryLatency time.Duration
	MaxDeliveryLatency time.Duration
	P99DeliveryLatency time.Duration
}

func avg(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range ds {
		total += d
	}
	return total / time.Duration(len(ds))
}

func max(ds []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	// Simple O(n) approximation — good enough for our use case
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	sortDurations(sorted)
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func sortDurations(ds []time.Duration) {
	// insertion sort — fine for typical watch metric sizes
	for i := 1; i < len(ds); i++ {
		key := ds[i]
		j := i - 1
		for j >= 0 && ds[j] > key {
			ds[j+1] = ds[j]
			j--
		}
		ds[j+1] = key
	}
}
