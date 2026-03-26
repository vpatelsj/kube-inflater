package watchstress

import (
	"testing"
	"time"
)

func TestMetricsSummary(t *testing.T) {
	m := &Metrics{
		TotalEvents: 1000,
		Reconnects:  5,
		Errors:      2,
		StartTime:   time.Now(),
	}

	m.AddConnectLatency(10 * time.Millisecond)
	m.AddConnectLatency(20 * time.Millisecond)
	m.AddConnectLatency(30 * time.Millisecond)

	m.AddDeliveryLatency(5 * time.Millisecond)
	m.AddDeliveryLatency(15 * time.Millisecond)
	m.AddDeliveryLatency(25 * time.Millisecond)
	m.AddDeliveryLatency(100 * time.Millisecond)

	m.EndTime = m.StartTime.Add(10 * time.Second)

	s := m.Summary()

	if s.TotalEvents != 1000 {
		t.Errorf("TotalEvents: got %d, want 1000", s.TotalEvents)
	}
	if s.EventsPerSecond != 100 {
		t.Errorf("EventsPerSecond: got %.1f, want 100", s.EventsPerSecond)
	}
	if s.Reconnects != 5 {
		t.Errorf("Reconnects: got %d, want 5", s.Reconnects)
	}
	if s.Errors != 2 {
		t.Errorf("Errors: got %d, want 2", s.Errors)
	}

	// Check connect latencies
	if s.AvgConnectLatency != 20*time.Millisecond {
		t.Errorf("AvgConnectLatency: got %v, want 20ms", s.AvgConnectLatency)
	}
	if s.MaxConnectLatency != 30*time.Millisecond {
		t.Errorf("MaxConnectLatency: got %v, want 30ms", s.MaxConnectLatency)
	}

	// Check delivery latencies
	expectedAvg := (5 + 15 + 25 + 100) * time.Millisecond / 4
	if s.AvgDeliveryLatency != expectedAvg {
		t.Errorf("AvgDeliveryLatency: got %v, want %v", s.AvgDeliveryLatency, expectedAvg)
	}
	if s.MaxDeliveryLatency != 100*time.Millisecond {
		t.Errorf("MaxDeliveryLatency: got %v, want 100ms", s.MaxDeliveryLatency)
	}
	// P99 with 4 elements: index = int(3*0.99) = 2 → sorted[2] = 25ms
	if s.P99DeliveryLatency != 25*time.Millisecond {
		t.Errorf("P99DeliveryLatency: got %v, want 25ms", s.P99DeliveryLatency)
	}
}

func TestMetricsEmpty(t *testing.T) {
	m := &Metrics{
		StartTime: time.Now(),
		EndTime:   time.Now().Add(time.Second),
	}
	s := m.Summary()
	if s.AvgConnectLatency != 0 {
		t.Errorf("expected zero AvgConnectLatency for empty metrics")
	}
	if s.AvgDeliveryLatency != 0 {
		t.Errorf("expected zero AvgDeliveryLatency for empty metrics")
	}
}

func TestSortDurations(t *testing.T) {
	ds := []time.Duration{30, 10, 50, 20, 40}
	sortDurations(ds)
	for i := 1; i < len(ds); i++ {
		if ds[i] < ds[i-1] {
			t.Errorf("not sorted at index %d: %v", i, ds)
			break
		}
	}
}
