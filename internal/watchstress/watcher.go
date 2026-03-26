package watchstress

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"

	"kube-inflater/internal/resourcegen"
)

// Watcher manages concurrent watch connections for stress testing.
type Watcher struct {
	dynClient dynamic.Interface
	cfg       Config
}

// NewWatcher creates a new watch stress tester.
func NewWatcher(dynClient dynamic.Interface, cfg Config) *Watcher {
	return &Watcher{dynClient: dynClient, cfg: cfg}
}

// RunStandalone opens N concurrent watch connections and measures throughput.
func (w *Watcher) RunStandalone(ctx context.Context) (*Metrics, error) {
	logInfo(fmt.Sprintf("Starting standalone watch stress: %d connections, duration %v", w.cfg.Connections, w.cfg.Duration))

	metrics := &Metrics{StartTime: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, w.cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	var totalEvents atomic.Int64
	var reconnects atomic.Int64
	var errors atomic.Int64

	for i := 0; i < w.cfg.Connections; i++ {
		wg.Add(1)
		typeName := w.cfg.ResourceTypes[i%len(w.cfg.ResourceTypes)]
		go func(connID int, typeName string) {
			defer wg.Done()
			events, reconn, errs := w.watchLoop(ctx, connID, typeName, metrics)
			totalEvents.Add(events)
			reconnects.Add(reconn)
			errors.Add(errs)
		}(i, typeName)
	}

	wg.Wait()

	metrics.TotalEvents = totalEvents.Load()
	metrics.Reconnects = reconnects.Load()
	metrics.Errors = errors.Load()
	metrics.EndTime = time.Now()

	return metrics, nil
}

// RunCombined opens watches first, then signals readiness for resource creation.
// The caller should start creating resources after watchReady is closed.
func (w *Watcher) RunCombined(ctx context.Context, watchReady chan<- struct{}) (*Metrics, error) {
	logInfo(fmt.Sprintf("Starting combined watch+create stress: %d connections, duration %v", w.cfg.Connections, w.cfg.Duration))

	metrics := &Metrics{StartTime: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, w.cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	var totalEvents atomic.Int64
	var reconnects atomic.Int64
	var errors atomic.Int64

	// Start all watchers
	readyCount := atomic.Int32{}
	for i := 0; i < w.cfg.Connections; i++ {
		wg.Add(1)
		typeName := w.cfg.ResourceTypes[i%len(w.cfg.ResourceTypes)]
		go func(connID int, typeName string) {
			defer wg.Done()
			readyCount.Add(1)
			if int(readyCount.Load()) == w.cfg.Connections {
				close(watchReady)
			}
			events, reconn, errs := w.watchLoopWithDelivery(ctx, connID, typeName, metrics)
			totalEvents.Add(events)
			reconnects.Add(reconn)
			errors.Add(errs)
		}(i, typeName)
	}

	wg.Wait()

	metrics.TotalEvents = totalEvents.Load()
	metrics.Reconnects = reconnects.Load()
	metrics.Errors = errors.Load()
	metrics.EndTime = time.Now()

	return metrics, nil
}

func (w *Watcher) watchLoop(ctx context.Context, connID int, typeName string, metrics *Metrics) (events, reconnects, errors int64) {
	gen, err := resourcegen.NewGenerator(typeName, 0)
	if err != nil {
		logWarn(fmt.Sprintf("Watch conn %d: unknown type %s", connID, typeName))
		return 0, 0, 1
	}

	gvr := gen.GVR()

	for {
		if ctx.Err() != nil {
			return
		}

		start := time.Now()
		var watcher watch.Interface
		var watchErr error

		if gen.IsNamespaced() && w.cfg.Namespace != "" {
			// Watch across a spread namespace
			nsIdx := connID % w.cfg.SpreadCount
			ns := fmt.Sprintf("%s-%d", w.cfg.Namespace, nsIdx)
			watcher, watchErr = w.dynClient.Resource(gvr).Namespace(ns).Watch(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app=%s", resourcegen.AppLabel),
			})
		} else {
			watcher, watchErr = w.dynClient.Resource(gvr).Watch(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app=%s", resourcegen.AppLabel),
			})
		}

		if watchErr != nil {
			errors++
			logWarn(fmt.Sprintf("Watch conn %d: error: %v", connID, watchErr))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			reconnects++
			continue
		}

		metrics.AddConnectLatency(time.Since(start))

		for event := range watcher.ResultChan() {
			if ctx.Err() != nil {
				watcher.Stop()
				return
			}
			if event.Type == watch.Error {
				errors++
				continue
			}
			events++
		}

		// ResultChan closed — reconnect
		reconnects++
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (w *Watcher) watchLoopWithDelivery(ctx context.Context, connID int, typeName string, metrics *Metrics) (events, reconnects, errors int64) {
	gen, err := resourcegen.NewGenerator(typeName, 0)
	if err != nil {
		return 0, 0, 1
	}

	gvr := gen.GVR()

	for {
		if ctx.Err() != nil {
			return
		}

		start := time.Now()
		var watcher watch.Interface
		var watchErr error

		if gen.IsNamespaced() && w.cfg.Namespace != "" {
			nsIdx := connID % w.cfg.SpreadCount
			ns := fmt.Sprintf("%s-%d", w.cfg.Namespace, nsIdx)
			watcher, watchErr = w.dynClient.Resource(gvr).Namespace(ns).Watch(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app=%s", resourcegen.AppLabel),
			})
		} else {
			watcher, watchErr = w.dynClient.Resource(gvr).Watch(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app=%s", resourcegen.AppLabel),
			})
		}

		if watchErr != nil {
			errors++
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			reconnects++
			continue
		}

		metrics.AddConnectLatency(time.Since(start))

		for event := range watcher.ResultChan() {
			receiveTime := time.Now()
			if ctx.Err() != nil {
				watcher.Stop()
				return
			}
			if event.Type == watch.Error {
				errors++
				continue
			}
			events++

			// Measure delivery latency for ADDED events
			if event.Type == watch.Added {
				if obj, ok := event.Object.(metav1.ObjectMetaAccessor); ok {
					createTime := obj.GetObjectMeta().GetCreationTimestamp().Time
					if !createTime.IsZero() {
						metrics.AddDeliveryLatency(receiveTime.Sub(createTime))
					}
				}
			}
		}

		reconnects++
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// PrintSummary logs the watch metrics summary.
func PrintSummary(s MetricsSummary) {
	logInfo("=== Watch Stress Results ===")
	logInfo(fmt.Sprintf("  Duration:            %v", s.Duration))
	logInfo(fmt.Sprintf("  Total Events:        %d", s.TotalEvents))
	logInfo(fmt.Sprintf("  Events/sec:          %.1f", s.EventsPerSecond))
	logInfo(fmt.Sprintf("  Reconnects:          %d", s.Reconnects))
	logInfo(fmt.Sprintf("  Errors:              %d", s.Errors))
	if s.AvgConnectLatency > 0 {
		logInfo(fmt.Sprintf("  Avg Connect Latency: %v", s.AvgConnectLatency))
		logInfo(fmt.Sprintf("  Max Connect Latency: %v", s.MaxConnectLatency))
	}
	if s.AvgDeliveryLatency > 0 {
		logInfo(fmt.Sprintf("  Avg Delivery Latency: %v", s.AvgDeliveryLatency))
		logInfo(fmt.Sprintf("  Max Delivery Latency: %v", s.MaxDeliveryLatency))
		logInfo(fmt.Sprintf("  P99 Delivery Latency: %v", s.P99DeliveryLatency))
	}
}

func logInfo(msg string) {
	fmt.Printf("[INFO] %s\n", msg)
}

func logWarn(msg string) {
	fmt.Printf("[WARN] %s\n", msg)
}
