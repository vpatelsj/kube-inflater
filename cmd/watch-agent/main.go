package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"kube-inflater/internal/watchstress"
)

func main() {
	var (
		connections  int
		durationSec  int
		staggerSec   int
		resourceType string
		namespace    string
		spreadCount  int
		qps          float64
		burst        int
	)

	flag.IntVar(&connections, "connections", 1, "Number of watch connections this agent opens")
	flag.IntVar(&durationSec, "duration", 60, "Total run duration in seconds (stagger + hold)")
	flag.IntVar(&staggerSec, "stagger", 0, "Seconds over which to spread initial watch setup (0=all at once)")
	flag.StringVar(&resourceType, "resource-type", "customresources", "Resource type to watch")
	flag.StringVar(&namespace, "namespace", "stress-test", "Namespace prefix for namespaced resources")
	flag.IntVar(&spreadCount, "spread-count", 10, "Number of spread namespaces to watch")
	flag.Float64Var(&qps, "qps", 1000000, "Client QPS (set very high to disable rate limiting)")
	flag.IntVar(&burst, "burst", 1000000, "Client burst (set very high to disable rate limiting)")
	flag.Parse()

	restConfig, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load kubeconfig: %v\n", err)
		os.Exit(1)
	}
	restConfig.QPS = float32(qps)
	restConfig.Burst = burst

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create dynamic client: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	watchCfg := watchstress.Config{
		Connections:     connections,
		Duration:        time.Duration(durationSec) * time.Second,
		StaggerDuration: time.Duration(staggerSec) * time.Second,
		ResourceTypes:   strings.Split(resourceType, ","),
		Mode:            watchstress.ModeStandalone,
		Namespace:       namespace,
		SpreadCount:     spreadCount,
	}

	watcher := watchstress.NewWatcher(dynClient, watchCfg)
	metrics, err := watcher.RunStandalone(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		os.Exit(1)
	}

	summary := metrics.Summary()
	result := map[string]interface{}{
		"pod":                os.Getenv("POD_NAME"),
		"events":             summary.TotalEvents,
		"events_per_sec":     summary.EventsPerSecond,
		"reconnects":         summary.Reconnects,
		"errors":             summary.Errors,
		"peak_alive_watches": summary.PeakAliveWatches,
		"avg_connect_ms":     float64(summary.AvgConnectLatency) / float64(time.Millisecond),
		"max_connect_ms":     float64(summary.MaxConnectLatency) / float64(time.Millisecond),
		"duration_sec":       summary.Duration.Seconds(),
		"connections":        connections,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode result: %v\n", err)
		os.Exit(1)
	}
}

// loadConfig tries in-cluster first, falls back to kubeconfig for local testing.
func loadConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}
