package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/inflater"
	"kube-inflater/internal/kwok"
	"kube-inflater/internal/resourcegen"
	"kube-inflater/internal/watchstress"
)

func main() {
	cfg := loadConfig()

	logInfo("🏗️  Starting kube-resource-inflater")
	logInfo(fmt.Sprintf("Run ID: %s", cfg.RunID))
	logInfo(fmt.Sprintf("Resource types: %v", cfg.ResourceTypes))
	logInfo(fmt.Sprintf("Count per type: %d | Workers: %d | QPS: %.0f | Burst: %d",
		cfg.CountPerType, cfg.Workers, cfg.QPS, cfg.Burst))

	// Build clients
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		logErr(fmt.Sprintf("Failed loading kubeconfig: %v", err))
		os.Exit(1)
	}

	restConfig.QPS = cfg.QPS
	restConfig.Burst = cfg.Burst

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		logErr(fmt.Sprintf("Failed creating clientset: %v", err))
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		logErr(fmt.Sprintf("Failed creating dynamic client: %v", err))
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logInfo("Received shutdown signal, stopping...")
		cancel()
	}()

	// Cleanup-only mode
	if cfg.CleanupOnly {
		cleanup := inflater.NewCleanup(clientset, dynClient, cfg)
		if err := cleanup.Run(ctx); err != nil {
			logErr(fmt.Sprintf("Cleanup failed: %v", err))
			os.Exit(1)
		}
		// Also clean KWOK nodes if enabled
		if cfg.KWOKEnabled {
			prov := kwok.NewProvisioner(clientset, dynClient, cfg.RunID, cfg.DryRun)
			if err := prov.Cleanup(ctx, cfg.KWOKCleanup); err != nil {
				logWarn(fmt.Sprintf("KWOK cleanup: %v", err))
			}
		}
		return
	}

	// Watch-only mode
	if cfg.WatchOnly {
		if err := runWatchOnly(ctx, dynClient, cfg); err != nil {
			logErr(fmt.Sprintf("Watch stress failed: %v", err))
			os.Exit(1)
		}
		return
	}

	// Full workflow: ensure namespaces → create resources → watch stress → perf tests
	engine := inflater.NewEngine(clientset, dynClient, cfg)

	if err := engine.EnsureNamespaces(ctx); err != nil {
		logErr(fmt.Sprintf("Failed ensuring namespaces: %v", err))
		os.Exit(1)
	}

	if err := engine.EnsureKWOK(ctx); err != nil {
		logErr(fmt.Sprintf("Failed ensuring KWOK: %v", err))
		os.Exit(1)
	}

	if cfg.WatchEnabled {
		// Combined mode: start watches, then create
		watchCfg := watchstress.Config{
			Connections:   cfg.WatchConnections,
			Duration:      cfg.WatchDuration,
			ResourceTypes: effectiveWatchTypes(cfg),
			Mode:          watchstress.ModeCombined,
			Namespace:     cfg.Namespace,
			SpreadCount:   cfg.SpreadNamespaces,
		}
		watcher := watchstress.NewWatcher(dynClient, watchCfg)

		watchReady := make(chan struct{})
		var watchMetrics *watchstress.Metrics
		var watchErr error

		go func() {
			watchMetrics, watchErr = watcher.RunCombined(ctx, watchReady)
		}()

		// Wait for watches to be established
		select {
		case <-watchReady:
			logInfo("All watch connections established, starting resource creation...")
		case <-time.After(30 * time.Second):
			logWarn("Timeout waiting for watch connections, proceeding anyway...")
		case <-ctx.Done():
			return
		}

		if err := engine.Run(ctx); err != nil {
			logErr(fmt.Sprintf("Resource creation failed: %v", err))
			os.Exit(1)
		}

		// Wait for watch to finish (duration-based)
		logInfo("Waiting for watch stress duration to complete...")
		<-ctx.Done()

		if watchErr != nil {
			logWarn(fmt.Sprintf("Watch stress error: %v", watchErr))
		}
		if watchMetrics != nil {
			watchstress.PrintSummary(watchMetrics.Summary())
		}
	} else {
		// Creation only
		if err := engine.Run(ctx); err != nil {
			logErr(fmt.Sprintf("Resource creation failed: %v", err))
			os.Exit(1)
		}
	}

	logInfo("🏗️  kube-resource-inflater completed!")
}

func runWatchOnly(ctx context.Context, dynClient dynamic.Interface, cfg *cfgpkg.ResourceInflaterConfig) error {
	watchCfg := watchstress.Config{
		Connections:   cfg.WatchConnections,
		Duration:      cfg.WatchDuration,
		ResourceTypes: effectiveWatchTypes(cfg),
		Mode:          watchstress.ModeStandalone,
		Namespace:     cfg.Namespace,
		SpreadCount:   cfg.SpreadNamespaces,
	}
	watcher := watchstress.NewWatcher(dynClient, watchCfg)
	metrics, err := watcher.RunStandalone(ctx)
	if err != nil {
		return err
	}
	watchstress.PrintSummary(metrics.Summary())
	return nil
}

func effectiveWatchTypes(cfg *cfgpkg.ResourceInflaterConfig) []string {
	if len(cfg.WatchTypes) > 0 {
		return cfg.WatchTypes
	}
	return cfg.ResourceTypes
}

func loadConfig() *cfgpkg.ResourceInflaterConfig {
	cfg := &cfgpkg.ResourceInflaterConfig{
		CountPerType:     cfgpkg.DefaultResourceCount,
		Workers:          cfgpkg.DefaultWorkers,
		QPS:              cfgpkg.DefaultQPS,
		Burst:            cfgpkg.DefaultBurst,
		BatchInitial:     cfgpkg.DefaultBatchInitial,
		BatchFactor:      cfgpkg.DefaultBatchFactor,
		MaxBatches:       cfgpkg.DefaultMaxBatches,
		SpreadNamespaces: cfgpkg.DefaultSpreadNamespaces,
		DataSizeBytes:    cfgpkg.DefaultDataSizeBytes,
		BatchPause:       cfgpkg.DefaultBatchPause,
		Namespace:        cfgpkg.DefaultResourceNamespace,
		WatchConnections: cfgpkg.DefaultWatchConnections,
		WatchDuration:    cfgpkg.DefaultWatchDuration,
		KWOKNodes:        cfgpkg.DefaultKWOKNodes,
	}

	var resourceTypesCSV string
	var watchTypesCSV string
	qps := float64(cfg.QPS)

	flag.StringVar(&resourceTypesCSV, "resource-types", "configmaps", "Comma-separated resource types to create: "+strings.Join(resourcegen.AllTypeNames(), ", "))
	flag.IntVar(&cfg.CountPerType, "count", cfg.CountPerType, "Number of resources to create per type")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers, "Number of concurrent worker goroutines")
	flag.Float64Var(&qps, "qps", qps, "Client-go QPS limit")
	flag.IntVar(&cfg.Burst, "burst", cfg.Burst, "Client-go burst limit")
	flag.IntVar(&cfg.BatchInitial, "batch-initial", cfg.BatchInitial, "Initial batch size for exponential ramp-up")
	flag.IntVar(&cfg.BatchFactor, "batch-factor", cfg.BatchFactor, "Batch growth factor")
	flag.IntVar(&cfg.MaxBatches, "max-batches", cfg.MaxBatches, "Maximum number of batches")
	flag.IntVar(&cfg.SpreadNamespaces, "spread-namespaces", cfg.SpreadNamespaces, "Number of namespaces to spread resources across")
	flag.IntVar(&cfg.DataSizeBytes, "data-size", cfg.DataSizeBytes, "Data payload size in bytes for ConfigMaps/Secrets (max 1MB)")
	batchPauseSec := int(cfg.BatchPause / time.Second)
	flag.IntVar(&batchPauseSec, "batch-pause", batchPauseSec, "Seconds to pause between batches")
	flag.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "Base namespace for resource creation")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Log what would be created without creating resources")
	flag.BoolVar(&cfg.CleanupOnly, "cleanup-only", false, "Only delete resources from a previous run")
	flag.StringVar(&cfg.RunID, "run-id", "", "Run ID (for cleanup; auto-generated if empty)")
	flag.BoolVar(&cfg.WatchEnabled, "watch", false, "Enable watch stress testing after resource creation")
	flag.BoolVar(&cfg.WatchOnly, "watch-only", false, "Only run watch stress testing (no resource creation)")
	flag.IntVar(&cfg.WatchConnections, "watch-connections", cfg.WatchConnections, "Number of concurrent watch connections")
	watchDurSec := int(cfg.WatchDuration / time.Second)
	flag.IntVar(&watchDurSec, "watch-duration", watchDurSec, "Watch stress duration in seconds")
	flag.StringVar(&watchTypesCSV, "watch-types", "", "Resource types to watch (defaults to --resource-types)")
	flag.BoolVar(&cfg.KWOKEnabled, "kwok", false, "Use KWOK fake nodes for pods/jobs/statefulsets (avoids real kubelet load)")
	flag.IntVar(&cfg.KWOKNodes, "kwok-nodes", cfgpkg.DefaultKWOKNodes, "Number of KWOK fake nodes to provision")
	flag.BoolVar(&cfg.KWOKCleanup, "kwok-cleanup-controller", false, "Also remove the KWOK controller on cleanup")

	flag.Parse()

	cfg.BatchPause = time.Duration(batchPauseSec) * time.Second
	cfg.WatchDuration = time.Duration(watchDurSec) * time.Second
	cfg.QPS = float32(qps)

	// Parse resource types
	if resourceTypesCSV != "" {
		cfg.ResourceTypes = parseCSV(resourceTypesCSV)
	}
	if watchTypesCSV != "" {
		cfg.WatchTypes = parseCSV(watchTypesCSV)
	}

	// Validate resource types
	for _, t := range cfg.ResourceTypes {
		if _, err := resourcegen.NewGenerator(t, 0); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Clamp data size
	if cfg.DataSizeBytes > 1024*1024 {
		cfg.DataSizeBytes = 1024 * 1024
		logWarn("Data size clamped to 1MB (Kubernetes object size limit)")
	}

	// Generate run ID
	if cfg.RunID == "" {
		ts := time.Now().UTC().Format("20060102-150405")
		suffix := rand.Intn(10000)
		cfg.RunID = fmt.Sprintf("%s-%04d", ts, suffix)
	}

	return cfg
}

func parseCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func logInfo(msg string) {
	fmt.Printf("[INFO] %s\n", msg)
}

func logWarn(msg string) {
	fmt.Printf("[WARN] %s\n", msg)
}

func logErr(msg string) {
	fmt.Printf("[ERROR] %s\n", msg)
}
