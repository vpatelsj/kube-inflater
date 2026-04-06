package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"kube-inflater/internal/benchmarkio"
	"kube-inflater/internal/watchstress"
)

func main() {
	// Subcommand dispatch: "watch" (default), "mutate"
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "watch":
			os.Args = append(os.Args[:1], os.Args[2:]...)
			runWatch()
			return
		case "mutate":
			os.Args = append(os.Args[:1], os.Args[2:]...)
			runMutate()
			return
		}
	}
	// No subcommand or flags starting with "-" → default to watch (backward compat)
	runWatch()
}

func runWatch() {
	var (
		connections  int
		durationSec  int
		staggerSec   int
		resourceType string
		namespace    string
		spreadCount  int
		qps          float64
		burst        int
		jsonReportDir string
	)

	flag.IntVar(&connections, "connections", 1, "Number of watch connections this agent opens")
	flag.IntVar(&durationSec, "duration", 60, "Total run duration in seconds (stagger + hold)")
	flag.IntVar(&staggerSec, "stagger", 0, "Seconds over which to spread initial watch setup (0=all at once)")
	flag.StringVar(&resourceType, "resource-type", "customresources", "Resource type to watch")
	flag.StringVar(&namespace, "namespace", "stress-test", "Namespace prefix for namespaced resources")
	flag.IntVar(&spreadCount, "spread-count", 10, "Number of spread namespaces to watch")
	flag.Float64Var(&qps, "qps", 1000000, "Client QPS (set very high to disable rate limiting)")
	flag.IntVar(&burst, "burst", 1000000, "Client burst (set very high to disable rate limiting)")
	flag.StringVar(&jsonReportDir, "json-report-dir", "", "Directory to write JSON report (for benchmark-ui)")
	flag.Parse()

	dynClient := mustDynClient(qps, burst)

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
		"pod":                 os.Getenv("POD_NAME"),
		"events":              summary.TotalEvents,
		"events_per_sec":      summary.EventsPerSecond,
		"reconnects":          summary.Reconnects,
		"errors":              summary.Errors,
		"peak_alive_watches":  summary.PeakAliveWatches,
		"avg_connect_ms":      float64(summary.AvgConnectLatency) / float64(time.Millisecond),
		"max_connect_ms":      float64(summary.MaxConnectLatency) / float64(time.Millisecond),
		"avg_delivery_ms":     float64(summary.AvgDeliveryLatency) / float64(time.Millisecond),
		"max_delivery_ms":     float64(summary.MaxDeliveryLatency) / float64(time.Millisecond),
		"p99_delivery_ms":     float64(summary.P99DeliveryLatency) / float64(time.Millisecond),
		"min_events_per_conn": summary.MinEventsPerConn,
		"max_events_per_conn": summary.MaxEventsPerConn,
		"duration_sec":        summary.Duration.Seconds(),
		"connections":         connections,
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode result: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(resultJSON))

	// Push results to a ConfigMap for scalable collection
	pushResultsConfigMap(resultJSON)

	// Write JSON report for benchmark-ui
	if jsonReportDir != "" {
		runID := os.Getenv("RUN_ID")
		if runID == "" {
			runID = fmt.Sprintf("watch-%d", time.Now().Unix())
		}
		bioCfg := benchmarkio.WatchStressConfig{
			Connections:   connections,
			DurationSec:   durationSec,
			StaggerSec:    staggerSec,
			ResourceTypes: strings.Split(resourceType, ","),
			Namespace:     namespace,
			SpreadCount:   spreadCount,
		}
		path, writeErr := benchmarkio.WriteWatchStressReport(jsonReportDir, runID, bioCfg, &summary, nil)
		if writeErr != nil {
			fmt.Fprintf(os.Stderr, "json report: %v\n", writeErr)
		} else {
			fmt.Fprintf(os.Stderr, "json report: written to %s\n", path)
		}
	}
}

func pushResultsConfigMap(resultJSON []byte) {
	podName := os.Getenv("POD_NAME")
	runID := os.Getenv("RUN_ID")
	resultsNS := os.Getenv("RESULTS_NAMESPACE")
	if podName == "" || resultsNS == "" {
		return
	}

	restConfig, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configmap push: failed to load config: %v\n", err)
		return
	}
	restConfig.QPS = 100
	restConfig.Burst = 100

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configmap push: failed to create client: %v\n", err)
		return
	}

	labels := map[string]string{
		"app": "watch-agent",
	}
	if runID != "" {
		labels["run-id"] = runID
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "result-" + podName,
			Labels: labels,
		},
		Data: map[string]string{
			"result.json": string(resultJSON),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Retry with jitter to avoid ConfigMap push stampede when all pods finish simultaneously
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			jitter := time.Duration(500+rand.Intn(2000*(1<<attempt))) * time.Millisecond
			time.Sleep(jitter)
		}
		_, lastErr = clientset.CoreV1().ConfigMaps(resultsNS).Create(ctx, cm, metav1.CreateOptions{})
		if lastErr == nil {
			fmt.Fprintf(os.Stderr, "configmap push: created configmap result-%s in %s\n", podName, resultsNS)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "configmap push: failed after 5 attempts: %v\n", lastErr)
}

func runMutate() {
	var (
		rate        int
		durationSec int
		namespace   string
		spreadCount int
		dataSize    int
		batchSize   int
		qps         float64
		burst       int
	)

	flag.IntVar(&rate, "rate", 100, "Mutations per second (0 = unlimited)")
	flag.IntVar(&durationSec, "duration", 60, "Mutation duration in seconds")
	flag.StringVar(&namespace, "namespace", "stress-test", "Namespace prefix for namespaced resources")
	flag.IntVar(&spreadCount, "spread-count", 10, "Number of spread namespaces")
	flag.IntVar(&dataSize, "data-size", 1024, "Payload size in bytes for created/updated resources")
	flag.IntVar(&batchSize, "batch-size", 10, "Items per create/delete cycle")
	flag.Float64Var(&qps, "qps", 1000000, "Client QPS")
	flag.IntVar(&burst, "burst", 1000000, "Client burst")
	flag.Parse()

	dynClient := mustDynClient(qps, burst)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	cfg := watchstress.MutatorConfig{
		Rate:          rate,
		Duration:      time.Duration(durationSec) * time.Second,
		Namespace:     namespace,
		SpreadCount:   spreadCount,
		DataSizeBytes: dataSize,
		BatchSize:     batchSize,
	}

	mutator := watchstress.NewMutator(dynClient, cfg)
	summary, err := mutator.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mutator error: %v\n", err)
		os.Exit(1)
	}

	summary.Pod = os.Getenv("POD_NAME")

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "")
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode result: %v\n", err)
		os.Exit(1)
	}
}

func mustDynClient(qps float64, burst int) dynamic.Interface {
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
	return dynClient
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
