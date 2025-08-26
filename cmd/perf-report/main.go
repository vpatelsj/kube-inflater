package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"kube-inflater/internal/perfv2"
)

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	outputDir := flag.String("output-dir", ".", "directory to save the performance report")
	onlyCommon := flag.Bool("only-common", false, "test only common endpoints")
	noLimits := flag.Bool("no-limits", false, "disable response size limits (WARNING: may return very large responses)")

	flag.Parse()

	// Build kubernetes client
	var config *rest.Config
	var err error

	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building kubernetes config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating kubernetes client: %v\n", err)
		os.Exit(1)
	}

	// Create performance reporter
	ctx := context.Background()
	reporter := perfv2.NewPerformanceReporter(clientset, config, ctx, *noLimits)

	fmt.Println("🚀 Starting API Performance Test...")

	if *noLimits {
		fmt.Println("⚠️  WARNING: Running with --no-limits flag. This may result in very large responses and slower performance testing.")
		fmt.Println("    Use this mode to measure full data transfer performance rather than API latency.")
	} else {
		fmt.Println("ℹ️  Running in latency test mode (limit=1). Use --no-limits to test full response sizes.")
	}

	// Get cluster name for the report filename
	clusterName := reporter.GetClusterName()
	timestamp := time.Now().Format("2006-01-02_15-04-05")

	// Clean cluster name for filename
	safeClusterName := strings.ReplaceAll(clusterName, "/", "-")
	safeClusterName = strings.ReplaceAll(safeClusterName, ":", "-")
	safeClusterName = strings.ReplaceAll(safeClusterName, " ", "_")

	reportFilename := fmt.Sprintf("api-performance-%s-%s.md", safeClusterName, timestamp)
	reportPath := filepath.Join(*outputDir, reportFilename)

	// Run performance tests
	measurements, err := reporter.RunPerformanceTest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running performance test: %v\n", err)
		os.Exit(1)
	}

	// Filter to common endpoints if requested
	if *onlyCommon {
		fmt.Println("Filtering to common endpoints only...")
		discovery := perfv2.NewEndpointDiscovery(clientset, ctx)
		allEndpoints, err := discovery.DiscoverAllEndpoints()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering endpoints for filtering: %v\n", err)
			os.Exit(1)
		}

		commonEndpoints := discovery.GetCommonEndpoints(allEndpoints)
		commonResourceNames := make(map[string]bool)
		for _, ep := range commonEndpoints {
			commonResourceNames[ep.Resource] = true
		}

		var filteredMeasurements []perfv2.LatencyMeasurement
		for _, m := range measurements {
			if commonResourceNames[m.Endpoint.Resource] {
				filteredMeasurements = append(filteredMeasurements, m)
			}
		}
		measurements = filteredMeasurements
		fmt.Printf("Filtered to %d common endpoints\n", len(measurements))
	}

	// Generate markdown report
	fmt.Println("📊 Generating performance report...")
	reportContent := reporter.GenerateMarkdownReport(measurements)

	// Save report to file
	err = os.WriteFile(reportPath, []byte(reportContent), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing report to file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Performance report saved to: %s\n", reportPath)

	// Print summary to console
	successful := 0
	failed := 0
	for _, m := range measurements {
		if m.Success {
			successful++
		} else {
			failed++
		}
	}

	fmt.Printf("\n📈 Summary:\n")
	fmt.Printf("  • Total endpoints tested: %d\n", len(measurements))
	fmt.Printf("  • Successful requests: %d\n", successful)
	fmt.Printf("  • Failed requests: %d\n", failed)
	fmt.Printf("  • Success rate: %.1f%%\n", float64(successful)/float64(len(measurements))*100)
	fmt.Printf("  • Report file: %s\n", reportPath)
}
