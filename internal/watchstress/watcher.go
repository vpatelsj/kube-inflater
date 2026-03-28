package watchstress

import (
	"context"
	"fmt"
	"math/rand"
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
	logInfo(fmt.Sprintf("Starting standalone watch stress: %d connections, stagger %v, duration %v", w.cfg.Connections, w.cfg.StaggerDuration, w.cfg.Duration))

	metrics := &Metrics{StartTime: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, w.cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	var totalEvents atomic.Int64
	var reconnects atomic.Int64
	var errors atomic.Int64

	// Launch all goroutines immediately; each one computes its own stagger delay.
	for i := 0; i < w.cfg.Connections; i++ {
		wg.Add(1)
		typeName := w.cfg.ResourceTypes[i%len(w.cfg.ResourceTypes)]
		var delay time.Duration
		if w.cfg.StaggerDuration > 0 && w.cfg.Connections > 1 {
			delay = time.Duration(int64(i) * int64(w.cfg.StaggerDuration) / int64(w.cfg.Connections))
		}
		go func(connID int, typeName string, staggerDelay time.Duration) {
			defer wg.Done()
			// Wait for our turn to avoid thundering herd at watch setup
			if staggerDelay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(staggerDelay):
				}
			}
			events, reconn, errs := w.watchLoop(ctx, connID, typeName, metrics)
			totalEvents.Add(events)
			reconnects.Add(reconn)
			errors.Add(errs)
		}(i, typeName, delay)
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

	// Start watchers in batches to avoid rate limiter stampede
	readyCount := atomic.Int32{}
	batchSize := 500
	if batchSize > w.cfg.Connections {
		batchSize = w.cfg.Connections
	}
	launched := 0
	for launched < w.cfg.Connections {
		end := launched + batchSize
		if end > w.cfg.Connections {
			end = w.cfg.Connections
		}
		for i := launched; i < end; i++ {
			wg.Add(1)
			typeName := w.cfg.ResourceTypes[i%len(w.cfg.ResourceTypes)]
			go func(connID int, typeName string) {
				defer wg.Done()
				if int(readyCount.Add(1)) == w.cfg.Connections {
					close(watchReady)
				}
				events, reconn, errs := w.watchLoopWithDelivery(ctx, connID, typeName, metrics)
				totalEvents.Add(events)
				reconnects.Add(reconn)
				errors.Add(errs)
			}(i, typeName)
		}
		launched = end
		if launched < w.cfg.Connections {
			time.Sleep(100 * time.Millisecond)
		}
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
			backoff := time.Duration(1000+rand.Intn(4000)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			reconnects++
			continue
		}

		metrics.AddConnectLatency(time.Since(start))
		metrics.WatchOpened()

		for event := range watcher.ResultChan() {
			if ctx.Err() != nil {
				watcher.Stop()
				metrics.WatchClosed()
				return
			}
			if event.Type == watch.Error {
				errors++
				continue
			}
			events++
		}

		metrics.WatchClosed()
		// ResultChan closed — reconnect with jitter to avoid stampede
		reconnects++
		backoff := time.Duration(500+rand.Intn(2500)) * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
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
	logInfo(fmt.Sprintf("  Peak Alive Watches:  %d", s.PeakAliveWatches))
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
