package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

	filterVerbs := flag.String("filter-verbs", "", "comma-separated list of verbs to filter by (e.g., 'list,get')")
	onlyCommon := flag.Bool("only-common", false, "show only common endpoints")

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

	// Create endpoint discovery
	ctx := context.Background()
	discovery := perfv2.NewEndpointDiscovery(clientset, ctx)

	// Discover all endpoints
	fmt.Println("Discovering API server endpoints...")
	endpoints, err := discovery.DiscoverAllEndpoints()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering endpoints: %v\n", err)
		os.Exit(1)
	}

	// Apply filters if requested
	if *filterVerbs != "" {
		// TODO: Parse comma-separated verbs and filter
		fmt.Printf("Filtering by verbs: %s\n", *filterVerbs)
	}

	if *onlyCommon {
		endpoints = discovery.GetCommonEndpoints(endpoints)
		fmt.Println("Showing only common endpoints...")
	}

	// Print results
	discovery.PrintEndpoints(endpoints)

	fmt.Printf("\nSummary: Found %d endpoints\n", len(endpoints))
}
