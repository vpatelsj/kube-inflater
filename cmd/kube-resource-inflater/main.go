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
	logInfo("Running API performance tests...")
	measurements, err := reporter.RunPerformanceTest()
	if err != nil {
		logWarn(fmt.Sprintf("API performance test failed: %v (report will be generated without API metrics)", err))
	} else {
		report.APIMeasurements = measurements
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
	flag.IntVar(&cfg.KWOKNodes, "kwok-nodes", cfgpkg.DefaultKWOKNodes, "Number of KWOK fake nodes to provision (auto-scaled up if needed)")
	flag.BoolVar(&cfg.KWOKCleanup, "kwok-cleanup-controller", false, "Also remove the KWOK controller on cleanup")
	flag.BoolVar(&benchmarkReport, "benchmark-report", false, "Generate a combined benchmark report after resource creation")
	flag.BoolVar(&jsonReport, "json-report", true, "Generate a JSON benchmark report (for benchmark-ui); use --json-report=false to disable")
	flag.StringVar(&reportOutputDir, "report-output-dir", "./benchmark-reports", "Directory to save the benchmark report")

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
	tokenAudStr := ""
	flag.StringVar(&tokenAudStr, "token-audiences", "", "Comma-separated ServiceAccount token audiences for hollow-node kubeconfig (default: auto)")

	flag.Parse()

	cfg.BatchPause = time.Duration(batchPauseSec) * time.Second
	cfg.QPS = float32(qps)

	// Parse resource types
	if resourceTypesCSV != "" {
		cfg.ResourceTypes = parseCSV(resourceTypesCSV)
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
