package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"kube-inflater/internal/benchmarkio"
	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/inflater"
	"kube-inflater/internal/kwok"
	"kube-inflater/internal/perfv2"
	"kube-inflater/internal/resourcegen"
	"kube-inflater/internal/watchdeploy"
)

func main() {
	cfg, benchmarkReport, jsonReport, reportOutputDir := loadConfig()

	logInfo("🏗️  Starting kube-resource-inflater")
	logInfo(fmt.Sprintf("Run ID: %s", cfg.RunID))
	logInfo(fmt.Sprintf("Resource types: %v", cfg.ResourceTypes))
	logInfo(fmt.Sprintf("Count per type: %d | Workers: %d | QPS: %.0f | Burst: %d",
		cfg.CountPerType, cfg.Workers, cfg.QPS, cfg.Burst))
	if cfg.HasPodBearingTypes() {
		logInfo("KWOK auto-enabled for pod-bearing resource types")
	}
	if cfg.HasWatchPhase() {
		durLabel := "continuous"
		if cfg.WatchDuration > 0 {
			durLabel = cfg.WatchDuration.String()
		}
		logInfo(fmt.Sprintf("Watch phase: %d connections, %s, mutator %d ops/s",
			cfg.WatchConnections, durLabel, cfg.MutatorRate))
	}

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
	if cfg.CleanupAll {
		logInfo("🧹 Nuclear cleanup: removing ALL kube-inflater resources")
		cleanup := inflater.NewCleanup(clientset, dynClient, cfg)
		if err := cleanup.RunAll(ctx); err != nil {
			logErr(fmt.Sprintf("Full cleanup failed: %v", err))
			os.Exit(1)
		}
		// Delete local report files
		for _, dir := range []string{reportOutputDir, "/tmp/benchmark-reports"} {
			files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
			if len(files) > 0 {
				logInfo(fmt.Sprintf("Deleting %d report files from %s", len(files), dir))
				for _, f := range files {
					_ = os.Remove(f)
				}
			}
		}
		return
	}

	if cfg.CleanupOnly {
		cleanup := inflater.NewCleanup(clientset, dynClient, cfg)
		if err := cleanup.Run(ctx); err != nil {
			logErr(fmt.Sprintf("Cleanup failed: %v", err))
			os.Exit(1)
		}
		// Also clean KWOK nodes if pod-bearing types were used
		prov := kwok.NewProvisioner(clientset, dynClient, cfg.RunID, cfg.DryRun)
		if err := prov.Cleanup(ctx, cfg.KWOKCleanup); err != nil {
			logWarn(fmt.Sprintf("KWOK cleanup: %v", err))
		}
		return
	}

	// Full workflow: ensure namespaces → create resources → perf tests
	engine := inflater.NewEngine(clientset, dynClient, cfg)

	if err := engine.EnsureNamespaces(ctx); err != nil {
		logErr(fmt.Sprintf("Failed ensuring namespaces: %v", err))
		os.Exit(1)
	}

	// Watch stress phase — deploy watch-agent Jobs in-cluster, they run throughout resource creation
	var watchDeployer *watchdeploy.Deployer
	if cfg.HasWatchPhase() {
		logInfo(fmt.Sprintf("📡 Deploying watch-agent: %d connections, stagger %v, mutator %d ops/s (in-cluster)",
			cfg.WatchConnections, cfg.WatchStagger, cfg.MutatorRate))

		watchTypes := cfg.WatchTypes
		if len(watchTypes) == 0 {
			watchTypes = []string{"customresources"}
		}

		watchDeployer = watchdeploy.NewDeployer(clientset, watchdeploy.Config{
			Image:            cfg.WatchAgentImage,
			Connections:      cfg.WatchConnections,
			Stagger:          cfg.WatchStagger,
			WatchTypes:       watchTypes,
			StressNamespace:  cfg.Namespace,
			SpreadCount:      cfg.SpreadNamespaces,
			MutatorRate:      cfg.MutatorRate,
			MutatorBatchSize: cfg.MutatorBatchSize,
			DataSizeBytes:    cfg.DataSizeBytes,
			RunID:            cfg.RunID,
			DryRun:           cfg.DryRun,
		})

		if err := watchDeployer.Deploy(ctx); err != nil {
			logErr(fmt.Sprintf("Watch deploy failed: %v", err))
			os.Exit(1)
		}
	}

	if err := engine.EnsureKWOK(ctx); err != nil {
		logErr(fmt.Sprintf("Failed ensuring KWOK: %v", err))
		os.Exit(1)
	}

	if err := engine.Run(ctx); err != nil {
		logErr(fmt.Sprintf("Resource creation failed: %v", err))
		os.Exit(1)
	}

	// Verify resources actually exist in the cluster
	if err := engine.Verify(ctx); err != nil {
		logErr(fmt.Sprintf("Verification failed: %v", err))
		os.Exit(1)
	}

	// Stop watch-agent Jobs, collect results, write report
	if watchDeployer != nil {
		logInfo("📡 Waiting for watch-agent pods to complete, collecting results...")
		result, err := watchDeployer.WaitAndCollect(ctx)
		if err != nil {
			logWarn(fmt.Sprintf("Watch collect failed: %v", err))
		}
		if result != nil && result.WatchSummary != nil {
			ws := result.WatchSummary
			logInfo(fmt.Sprintf("📡 Watch results: %d events (%.0f/s), %d reconnects, %d errors, peak %d watches",
				ws.TotalEvents, ws.EventsPerSecond, ws.Reconnects, ws.Errors, ws.PeakAliveWatches))
		}
		if result != nil && result.MutatorSummary != nil {
			ms := result.MutatorSummary
			logInfo(fmt.Sprintf("📡 Mutator results: %d creates, %d updates, %d deletes (%.1f ops/s)",
				ms.Creates, ms.Updates, ms.Deletes, ms.ActualRate))
		}

		// Write watch stress JSON report
		if jsonReport && result != nil && result.WatchSummary != nil {
			watchTypes := cfg.WatchTypes
			if len(watchTypes) == 0 {
				watchTypes = []string{"customresources"}
			}
			bioCfg := benchmarkio.WatchStressConfig{
				Connections:   cfg.WatchConnections,
				DurationSec:   int(result.WatchSummary.Duration.Seconds()),
				StaggerSec:    int(cfg.WatchStagger.Seconds()),
				ResourceTypes: watchTypes,
				Namespace:     cfg.Namespace,
				SpreadCount:   cfg.SpreadNamespaces,
			}
			path, writeErr := benchmarkio.WriteWatchStressReport(reportOutputDir, cfg.RunID, bioCfg, result.WatchSummary, result.MutatorSummary)
			if writeErr != nil {
				logErr(fmt.Sprintf("Failed writing watch stress report: %v", writeErr))
			} else {
				logInfo(fmt.Sprintf("Watch stress report written to %s", path))
			}
		}
	}

	// Generate benchmark report if requested
	if benchmarkReport {
		generateBenchmarkReport(engine, clientset, restConfig, ctx, cfg, reportOutputDir)
	}

	// Generate JSON report for benchmark-ui
	if jsonReport {
		generateJSONReport(engine, clientset, restConfig, ctx, cfg, reportOutputDir)
	}

	logInfo("🏗️  kube-resource-inflater completed!")
}

func generateBenchmarkReport(engine *inflater.Engine, clientset kubernetes.Interface, restConfig *rest.Config, ctx context.Context, cfg *cfgpkg.ResourceInflaterConfig, outputDir string) {
	logInfo("Generating benchmark report...")

	report := &perfv2.BenchmarkReport{
		RunID:            cfg.RunID,
		ResourceTypes:    cfg.ResourceTypes,
		CountPerType:     cfg.CountPerType,
		Workers:          cfg.Workers,
		QPS:              cfg.QPS,
		Burst:            cfg.Burst,
		SpreadNamespaces: cfg.SpreadNamespaces,
		KWOKNodes:        cfg.KWOKNodes,
		InflationResults: engine.Results,
	}

	// Gather cluster info
	reporter := perfv2.NewPerformanceReporter(clientset, restConfig, ctx, false)
	clusterInfo := reporter.GatherClusterInfo()
	report.ClusterInfo = &clusterInfo

	// Run API latency tests
	if !cfg.SkipPerfTests {
		logInfo("Running API performance tests...")
		measurements, err := reporter.RunPerformanceTest()
		if err != nil {
			logWarn(fmt.Sprintf("API performance test failed: %v (report will be generated without API metrics)", err))
		} else {
			report.APIMeasurements = measurements
		}
	}

	// Write report
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		logErr(fmt.Sprintf("Failed creating output directory: %v", err))
		return
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("benchmark-report-%s-%s.md", cfg.RunID, timestamp)
	reportPath := filepath.Join(outputDir, filename)

	f, err := os.Create(reportPath)
	if err != nil {
		logErr(fmt.Sprintf("Failed creating report file: %v", err))
		return
	}
	defer f.Close()

	report.RenderMarkdown(f)
	logInfo(fmt.Sprintf("Benchmark report written to %s", reportPath))
}

func generateJSONReport(engine *inflater.Engine, clientset kubernetes.Interface, restConfig *rest.Config, ctx context.Context, cfg *cfgpkg.ResourceInflaterConfig, outputDir string) {
	logInfo("Generating JSON benchmark report...")

	bioCfg := benchmarkio.ResourceCreationConfig{
		ResourceTypes:    cfg.ResourceTypes,
		CountPerType:     cfg.CountPerType,
		Workers:          cfg.Workers,
		QPS:              cfg.QPS,
		Burst:            cfg.Burst,
		BatchInitial:     cfg.BatchInitial,
		BatchFactor:      cfg.BatchFactor,
		MaxBatches:       cfg.MaxBatches,
		SpreadNamespaces: cfg.SpreadNamespaces,
		DataSizeBytes:    cfg.DataSizeBytes,
		KWOKNodes:        cfg.KWOKNodes,
	}

	reporter := perfv2.NewPerformanceReporter(clientset, restConfig, ctx, false)
	clusterInfo := reporter.GatherClusterInfo()

	var measurements []perfv2.LatencyMeasurement
	if !cfg.SkipPerfTests {
		logInfo("Running API performance tests for JSON report...")
		var err error
		measurements, err = reporter.RunPerformanceTest()
		if err != nil {
			logWarn(fmt.Sprintf("API performance test failed: %v", err))
		}
	}

	path, err := benchmarkio.WriteResourceCreationReport(outputDir, cfg.RunID, bioCfg, engine.Results, &clusterInfo, measurements)
	if err != nil {
		logErr(fmt.Sprintf("Failed writing JSON report: %v", err))
		return
	}
	logInfo(fmt.Sprintf("JSON benchmark report written to %s", path))
}

func loadConfig() (cfg *cfgpkg.ResourceInflaterConfig, benchmarkReport bool, jsonReport bool, reportOutputDir string) {
	cfg = &cfgpkg.ResourceInflaterConfig{
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
		KWOKNodes:        cfgpkg.DefaultKWOKNodes,
	}

	var resourceTypesCSV string
	var presetName string
	qps := float64(cfg.QPS)

	flag.StringVar(&presetName, "preset", "", "T-shirt size preset: small (10k), medium (50k), large (100k) of every resource type")
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
	flag.BoolVar(&cfg.CleanupAll, "cleanup-all", false, "Nuclear cleanup: delete ALL kube-inflater resources, KWOK, hollow nodes, namespaces, and CRDs")
	flag.StringVar(&cfg.RunID, "run-id", "", "Run ID (for cleanup; auto-generated if empty)")
	flag.IntVar(&cfg.KWOKNodes, "kwok-nodes", cfgpkg.DefaultKWOKNodes, "Number of KWOK fake nodes to provision (auto-scaled up if needed)")
	flag.BoolVar(&cfg.KWOKCleanup, "kwok-cleanup-controller", false, "Also remove the KWOK controller on cleanup")
	flag.BoolVar(&benchmarkReport, "benchmark-report", false, "Generate a combined benchmark report after resource creation")
	flag.BoolVar(&jsonReport, "json-report", true, "Generate a JSON benchmark report (for benchmark-ui); use --json-report=false to disable")
	flag.BoolVar(&cfg.SkipPerfTests, "skip-perf-tests", false, "Skip API latency tests in the JSON report")
	flag.StringVar(&reportOutputDir, "report-output-dir", "./benchmark-reports", "Directory to save the benchmark report")

	// Watch stress flags (set automatically by presets, can be overridden)
	watchDurationSec := 0
	watchStaggerSec := 0
	mutatorDurationSec := 0
	watchTypesCSV := ""
	flag.IntVar(&cfg.WatchConnections, "watch-connections", 0, "Number of concurrent watch connections (0=skip watch phase)")
	flag.IntVar(&watchDurationSec, "watch-duration", 0, "Watch phase duration in seconds (0=run until resource creation completes)")
	flag.IntVar(&watchStaggerSec, "watch-stagger", 0, "Seconds to stagger watch connection setup")
	flag.StringVar(&watchTypesCSV, "watch-types", "customresources", "Comma-separated resource types to watch")
	flag.StringVar(&cfg.WatchAgentImage, "watch-agent-image", "k3sacr1.azurecr.io/watch-agent:latest", "Container image for in-cluster watch-agent pods")
	flag.IntVar(&cfg.MutatorRate, "mutator-rate", 0, "Mutations per second during watch phase")
	flag.IntVar(&mutatorDurationSec, "mutator-duration", 0, "Mutator duration in seconds (defaults to watch-duration)")
	flag.IntVar(&cfg.MutatorBatchSize, "mutator-batch-size", 10, "Items per mutator create/update/delete cycle")

	// Hollow node flags (used when --resource-types includes "hollownodes")
	hollowOpts := &cfgpkg.HollowNodeOpts{
		ContainersPerPod:  cfgpkg.DefaultContainersPerPod,
		KubemarkImage:     cfgpkg.DefaultKubemarkImage,
		NodeStatusFreq:    "60s",
		NodeLeaseDuration: 240,
		NodeMonitorGrace:  "240s",
		Namespace:         cfgpkg.DefaultNamespace,
		WaitTimeout:       time.Duration(cfgpkg.DefaultWaitTimeoutSec) * time.Second,
		RetainDaemonSets:  1,
	}
	flag.IntVar(&hollowOpts.ContainersPerPod, "containers-per-pod", hollowOpts.ContainersPerPod, "Kubemark containers (hollow nodes) per DaemonSet pod")
	flag.StringVar(&hollowOpts.KubemarkImage, "kubemark-image", hollowOpts.KubemarkImage, "Kubemark container image")
	flag.StringVar(&hollowOpts.NodeStatusFreq, "node-status-frequency", hollowOpts.NodeStatusFreq, "Kubelet node status update frequency")
	flag.IntVar(&hollowOpts.NodeLeaseDuration, "node-lease-duration", hollowOpts.NodeLeaseDuration, "Node lease duration (seconds)")
	flag.StringVar(&hollowOpts.NodeMonitorGrace, "node-monitor-grace", hollowOpts.NodeMonitorGrace, "Node monitor grace period")
	flag.StringVar(&hollowOpts.Namespace, "hollow-namespace", hollowOpts.Namespace, "Namespace for hollow node DaemonSet and supporting resources")
	hollowWaitSec := int(hollowOpts.WaitTimeout / time.Second)
	flag.IntVar(&hollowWaitSec, "hollow-wait-timeout", hollowWaitSec, "Seconds to wait for hollow nodes to become ready")
	flag.BoolVar(&hollowOpts.PrunePrevious, "prune-previous", false, "Prune older hollow-node DaemonSets before creating a new one")
	flag.IntVar(&hollowOpts.RetainDaemonSets, "retain-daemonsets", hollowOpts.RetainDaemonSets, "Number of most recent hollow-node DaemonSets to retain when pruning")
	flag.StringVar(&hollowOpts.DaemonSetName, "daemonset-name", "", "Explicit DaemonSet name for hollow nodes (auto-generated if empty)")
	tokenAudStr := ""
	flag.StringVar(&tokenAudStr, "token-audiences", "", "Comma-separated ServiceAccount token audiences for hollow-node kubeconfig (default: auto)")

	flag.Parse()

	// Save explicitly-set flag values BEFORE ApplyPreset overwrites them.
	// flag.IntVar writes directly to cfg.*, so the preset clobbers those values.
	explicitFlags := make(map[string]string)
	flag.Visit(func(f *flag.Flag) {
		explicitFlags[f.Name] = f.Value.String()
	})

	// Apply preset first — individual flags override preset values
	if presetName != "" {
		if !cfg.ApplyPreset(presetName) {
			fmt.Fprintf(os.Stderr, "Error: unknown preset %q (valid: %s)\n", presetName, strings.Join(cfgpkg.PresetNames, ", "))
			os.Exit(1)
		}
		logInfo(fmt.Sprintf("Applied preset %q: %d per type, %d types, %d workers, QPS %.0f",
			presetName, cfg.CountPerType, len(cfg.ResourceTypes), cfg.Workers, cfg.QPS))
		// Update qps from preset (may be overridden below by explicit flag)
		qps = float64(cfg.QPS)
	}

	// Re-apply explicitly-set flags on top of preset
	for name, val := range explicitFlags {
		f := flag.Lookup(name)
		if f != nil {
			_ = f.Value.Set(val)
		}
	}

	cfg.BatchPause = time.Duration(batchPauseSec) * time.Second
	cfg.QPS = float32(qps)

	// Convert watch duration flags (explicit flags override preset values)
	if watchDurationSec > 0 {
		cfg.WatchDuration = time.Duration(watchDurationSec) * time.Second
	}
	if watchStaggerSec > 0 {
		cfg.WatchStagger = time.Duration(watchStaggerSec) * time.Second
	}
	if mutatorDurationSec > 0 {
		cfg.MutatorDuration = time.Duration(mutatorDurationSec) * time.Second
	} else if cfg.MutatorDuration == 0 && cfg.WatchDuration > 0 {
		cfg.MutatorDuration = cfg.WatchDuration
	}
	// Parse resource types: use explicit flag if set, otherwise keep preset value
	if _, ok := explicitFlags["resource-types"]; ok {
		cfg.ResourceTypes = parseCSV(resourceTypesCSV)
	} else if presetName == "" && resourceTypesCSV != "" {
		cfg.ResourceTypes = parseCSV(resourceTypesCSV)
	}

	// Parse watch types
	if _, ok := explicitFlags["watch-types"]; ok {
		cfg.WatchTypes = parseCSV(watchTypesCSV)
	} else if len(cfg.WatchTypes) == 0 && watchTypesCSV != "" {
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

	// Populate hollow node opts
	hollowOpts.WaitTimeout = time.Duration(hollowWaitSec) * time.Second
	if tokenAudStr != "" {
		hollowOpts.TokenAudiences = parseCSV(tokenAudStr)
	}
	cfg.HollowNodeOpts = hollowOpts
	cfg.HollowNodeWaitTimeout = hollowOpts.WaitTimeout

	return cfg, benchmarkReport, jsonReport, reportOutputDir
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
