package perfv2

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// LatencyMeasurement represents a single endpoint latency measurement
type LatencyMeasurement struct {
	Endpoint     APIEndpoint
	Verb         string // The HTTP verb used (GET, LIST, etc.)
	Latency      time.Duration
	Success      bool
	Error        string
	ResponseCode int
	ResponseSize int64 // Size of the response payload in bytes
	Timestamp    time.Time
}

// ClusterInfo holds information about the cluster
type ClusterInfo struct {
	NodeCount             int
	PodCount              int
	NamespaceCount        int
	ServiceCount          int
	DeploymentCount       int
	ConfigMapCount        int
	SecretCount           int
	PersistentVolumeCount int
	StorageClassCount     int
	IngressCount          int
}

// PerformanceReporter handles API endpoint performance testing and reporting
type PerformanceReporter struct {
	client    kubernetes.Interface
	config    *rest.Config
	discovery *EndpointDiscovery
	ctx       context.Context
	noLimits  bool // Whether to disable response size limits
}

// NewPerformanceReporter creates a new performance reporter
func NewPerformanceReporter(client kubernetes.Interface, config *rest.Config, ctx context.Context, noLimits bool) *PerformanceReporter {
	return &PerformanceReporter{
		client:    client,
		config:    config,
		discovery: NewEndpointDiscovery(client, ctx),
		ctx:       ctx,
		noLimits:  noLimits,
	}
}

// MeasureEndpointLatency performs a single request to an endpoint and measures latency
func (pr *PerformanceReporter) MeasureEndpointLatency(endpoint APIEndpoint, verb string) LatencyMeasurement {
	measurement := LatencyMeasurement{
		Endpoint:  endpoint,
		Verb:      verb,
		Success:   false,
		Timestamp: time.Now(),
	}

	// Build the request URL
	url := pr.buildRequestURL(endpoint)

	// Create REST client for the request
	restClient := pr.client.CoreV1().RESTClient()
	if !strings.HasPrefix(endpoint.GroupVersion, "v1") {
		// For non-core API groups, we need a different client
		// For now, we'll use the discovery client's REST client
		restClient = pr.client.Discovery().RESTClient()
	}

	// Measure latency
	start := time.Now()

	var result rest.Result

	// Choose the appropriate HTTP method based on the verb
	switch strings.ToUpper(verb) {
	case "LIST":
		// Perform the LIST request
		req := restClient.Get().AbsPath(url)
		if !pr.noLimits {
			// Add limit=1 to minimize response size for latency testing
			req = req.Param("limit", "1")
		}
		result = req.Do(pr.ctx)
	case "GET":
		// For GET, we need to append an item name, but since we don't know valid names,
		// we'll use LIST instead for consistency
		req := restClient.Get().AbsPath(url)
		if !pr.noLimits {
			// Add limit=1 to minimize response size for latency testing
			req = req.Param("limit", "1")
		}
		result = req.Do(pr.ctx)
	case "CREATE":
		// We won't actually create resources in performance testing
		// Just measure the time to get a validation error
		result = restClient.Post().
			AbsPath(url).
			Body([]byte(`{}`)).
			Do(pr.ctx)
	case "UPDATE":
		// Similar to CREATE, we expect this to fail but measure the time
		result = restClient.Put().
			AbsPath(url + "/dummy").
			Body([]byte(`{}`)).
			Do(pr.ctx)
	case "DELETE":
		// We won't actually delete resources, just measure validation time
		result = restClient.Delete().
			AbsPath(url + "/dummy").
			Do(pr.ctx)
	default:
		// Default to LIST operation
		req := restClient.Get().AbsPath(url)
		if !pr.noLimits {
			req = req.Param("limit", "1")
		}
		result = req.Do(pr.ctx)
	}

	measurement.Latency = time.Since(start)

	// Get response body to measure size
	var responseBody []byte
	var responseErr error
	responseBody, responseErr = result.Raw()

	// Calculate response size
	if responseErr == nil {
		measurement.ResponseSize = int64(len(responseBody))
	}

	// Check result
	if result.Error() != nil {
		measurement.Error = result.Error().Error()
		measurement.ResponseCode = 500 // Default error code

		// For CREATE/UPDATE/DELETE operations, we expect errors (since we're not providing valid data)
		// Consider these as "successful" latency measurements if they fail quickly due to validation
		if verb == "CREATE" || verb == "UPDATE" || verb == "DELETE" {
			if strings.Contains(measurement.Error, "is required") ||
				strings.Contains(measurement.Error, "not found") ||
				strings.Contains(measurement.Error, "invalid") {
				measurement.Success = true
				measurement.ResponseCode = 400 // Validation error is expected
			}
		}
	} else {
		measurement.Success = true
		measurement.ResponseCode = 200
	}

	return measurement
}

// buildRequestURL constructs the API URL for a given endpoint
func (pr *PerformanceReporter) buildRequestURL(endpoint APIEndpoint) string {
	if endpoint.Namespaced {
		// Use default namespace for namespaced resources
		if endpoint.GroupVersion == "v1" {
			return fmt.Sprintf("/api/v1/namespaces/default/%s", endpoint.Resource)
		}
		parts := strings.Split(endpoint.GroupVersion, "/")
		if len(parts) == 2 {
			group, version := parts[0], parts[1]
			return fmt.Sprintf("/apis/%s/%s/namespaces/default/%s", group, version, endpoint.Resource)
		}
	} else {
		// Cluster-scoped resource
		if endpoint.GroupVersion == "v1" {
			return fmt.Sprintf("/api/v1/%s", endpoint.Resource)
		}
		parts := strings.Split(endpoint.GroupVersion, "/")
		if len(parts) == 2 {
			group, version := parts[0], parts[1]
			return fmt.Sprintf("/apis/%s/%s/%s", group, version, endpoint.Resource)
		}
	}

	// Fallback
	return endpoint.FullPath
}

// RunPerformanceTest executes performance tests on all discoverable endpoints
func (pr *PerformanceReporter) RunPerformanceTest() ([]LatencyMeasurement, error) {
	fmt.Println("Discovering API endpoints...")
	endpoints, err := pr.discovery.DiscoverAllEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to discover endpoints: %w", err)
	}

	// Filter to only endpoints that support GET/LIST operations and determine preferred verb
	type EndpointWithVerb struct {
		Endpoint APIEndpoint
		Verb     string
	}

	var testableEndpoints []EndpointWithVerb
	for _, endpoint := range endpoints {
		hasGet := false
		hasList := false

		for _, verb := range endpoint.Verbs {
			switch verb {
			case "get":
				hasGet = true
			case "list":
				hasList = true
			}
		}

		// Prioritize LIST over GET for better performance testing
		if hasList {
			testableEndpoints = append(testableEndpoints, EndpointWithVerb{endpoint, "list"})
		} else if hasGet {
			testableEndpoints = append(testableEndpoints, EndpointWithVerb{endpoint, "get"})
		}
	}

	fmt.Printf("Testing %d endpoints for latency...\n", len(testableEndpoints))

	var measurements []LatencyMeasurement
	for i, endpointWithVerb := range testableEndpoints {
		if i%10 == 0 {
			fmt.Printf("Progress: %d/%d endpoints tested\n", i, len(testableEndpoints))
		}

		measurement := pr.MeasureEndpointLatency(endpointWithVerb.Endpoint, endpointWithVerb.Verb)
		measurements = append(measurements, measurement)

		// Small delay to avoid overwhelming the API server
		time.Sleep(10 * time.Millisecond)
	}

	fmt.Printf("Completed testing %d endpoints\n", len(measurements))
	return measurements, nil
}

// GetClusterName attempts to get a meaningful cluster name from the kubeconfig
func (pr *PerformanceReporter) GetClusterName() string {
	// For now, return a generic name based on timestamp
	// In the future, this could be enhanced to read from kubeconfig context
	return fmt.Sprintf("kubernetes-cluster-%d", time.Now().Unix())
}

// GatherClusterInfo collects basic information about the cluster
func (pr *PerformanceReporter) GatherClusterInfo() ClusterInfo {
	info := ClusterInfo{}

	// Get node count
	if nodes, err := pr.client.CoreV1().Nodes().List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.NodeCount = len(nodes.Items)
	}

	// Get pod count across all namespaces
	if pods, err := pr.client.CoreV1().Pods("").List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.PodCount = len(pods.Items)
	}

	// Get namespace count
	if namespaces, err := pr.client.CoreV1().Namespaces().List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.NamespaceCount = len(namespaces.Items)
	}

	// Get service count across all namespaces
	if services, err := pr.client.CoreV1().Services("").List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.ServiceCount = len(services.Items)
	}

	// Get deployment count across all namespaces
	if deployments, err := pr.client.AppsV1().Deployments("").List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.DeploymentCount = len(deployments.Items)
	}

	// Get configmap count across all namespaces
	if configMaps, err := pr.client.CoreV1().ConfigMaps("").List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.ConfigMapCount = len(configMaps.Items)
	}

	// Get secret count across all namespaces
	if secrets, err := pr.client.CoreV1().Secrets("").List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.SecretCount = len(secrets.Items)
	}

	// Get persistent volume count
	if pvs, err := pr.client.CoreV1().PersistentVolumes().List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.PersistentVolumeCount = len(pvs.Items)
	}

	// Get storage class count
	if storageClasses, err := pr.client.StorageV1().StorageClasses().List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.StorageClassCount = len(storageClasses.Items)
	}

	// Get ingress count across all namespaces
	if ingresses, err := pr.client.NetworkingV1().Ingresses("").List(pr.ctx, metav1.ListOptions{}); err == nil {
		info.IngressCount = len(ingresses.Items)
	}

	return info
}

// formatBytes converts a byte count to a human-readable string
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// getAPIGroupPriority returns a priority value for API groups to control sorting order
func getAPIGroupPriority(groupVersion string) int {
	switch {
	case groupVersion == "v1":
		return 0 // Highest priority for core v1 resources
	case strings.HasPrefix(groupVersion, "apps/v1"):
		return 1 // Second priority for apps/v1
	case strings.HasPrefix(groupVersion, "batch/v1"):
		return 2 // Third priority for batch/v1
	case strings.HasPrefix(groupVersion, "networking.k8s.io/v1"):
		return 3 // Fourth priority for networking
	case strings.HasPrefix(groupVersion, "rbac.authorization.k8s.io/v1"):
		return 4 // Fifth priority for RBAC
	case strings.HasSuffix(groupVersion, "/v1"):
		return 5 // Other v1 APIs
	case strings.HasSuffix(groupVersion, "/v1beta1"):
		return 6 // Beta APIs
	case strings.HasSuffix(groupVersion, "/v1alpha1"):
		return 7 // Alpha APIs
	default:
		return 8 // Everything else
	}
}

// GenerateMarkdownReport creates a markdown report from the measurements
func (pr *PerformanceReporter) GenerateMarkdownReport(measurements []LatencyMeasurement) string {
	clusterName := pr.GetClusterName()
	timestamp := time.Now().Format("2006-01-02 15:04:05 UTC")
	clusterInfo := pr.GatherClusterInfo()

	var report strings.Builder

	// Header
	report.WriteString(fmt.Sprintf("# API Performance Report - %s\n\n", clusterName))
	report.WriteString(fmt.Sprintf("**Generated:** %s  \n", timestamp))
	report.WriteString(fmt.Sprintf("**Cluster:** %s  \n", clusterName))
	report.WriteString(fmt.Sprintf("**Total Endpoints Tested:** %d  \n", len(measurements)))

	// Test configuration
	if pr.noLimits {
		report.WriteString("**Test Mode:** Full response data (no limits)  \n")
		report.WriteString("⚠️  *Response sizes represent full resource lists*  \n\n")
	} else {
		report.WriteString("**Test Mode:** Limited response data (limit=1)  \n")
		report.WriteString("ℹ️  *Response sizes represent single items for latency testing*  \n\n")
	}

	// Cluster Information
	report.WriteString("## Cluster Information\n\n")
	report.WriteString(fmt.Sprintf("- **Nodes:** %d\n", clusterInfo.NodeCount))
	report.WriteString(fmt.Sprintf("- **Pods:** %d\n", clusterInfo.PodCount))
	report.WriteString(fmt.Sprintf("- **Namespaces:** %d\n", clusterInfo.NamespaceCount))
	report.WriteString(fmt.Sprintf("- **Services:** %d\n", clusterInfo.ServiceCount))
	report.WriteString(fmt.Sprintf("- **Deployments:** %d\n", clusterInfo.DeploymentCount))
	report.WriteString(fmt.Sprintf("- **ConfigMaps:** %d\n", clusterInfo.ConfigMapCount))
	report.WriteString(fmt.Sprintf("- **Secrets:** %d\n", clusterInfo.SecretCount))
	report.WriteString(fmt.Sprintf("- **Persistent Volumes:** %d\n", clusterInfo.PersistentVolumeCount))
	report.WriteString(fmt.Sprintf("- **Storage Classes:** %d\n", clusterInfo.StorageClassCount))
	report.WriteString(fmt.Sprintf("- **Ingresses:** %d\n\n", clusterInfo.IngressCount))

	// Summary statistics
	successful := 0
	failed := 0
	var totalLatency time.Duration
	var minLatency, maxLatency time.Duration

	for i, m := range measurements {
		if m.Success {
			successful++
			totalLatency += m.Latency
			if i == 0 || m.Latency < minLatency {
				minLatency = m.Latency
			}
			if m.Latency > maxLatency {
				maxLatency = m.Latency
			}
		} else {
			failed++
		}
	}

	avgLatency := time.Duration(0)
	if successful > 0 {
		avgLatency = totalLatency / time.Duration(successful)
	}

	report.WriteString("## Performance Summary\n\n")
	report.WriteString(fmt.Sprintf("- **Successful Requests:** %d\n", successful))
	report.WriteString(fmt.Sprintf("- **Failed Requests:** %d\n", failed))
	report.WriteString(fmt.Sprintf("- **Success Rate:** %.1f%%\n", float64(successful)/float64(len(measurements))*100))
	report.WriteString(fmt.Sprintf("- **Average Latency:** %v\n", avgLatency))
	report.WriteString(fmt.Sprintf("- **Min Latency:** %v\n", minLatency))
	report.WriteString(fmt.Sprintf("- **Max Latency:** %v\n", maxLatency))
	report.WriteString("\n")

	// Sort measurements by API group priority, then by group version, then by resource
	sort.Slice(measurements, func(i, j int) bool {
		iPriority := getAPIGroupPriority(measurements[i].Endpoint.GroupVersion)
		jPriority := getAPIGroupPriority(measurements[j].Endpoint.GroupVersion)

		if iPriority != jPriority {
			return iPriority < jPriority
		}
		if measurements[i].Endpoint.GroupVersion != measurements[j].Endpoint.GroupVersion {
			return measurements[i].Endpoint.GroupVersion < measurements[j].Endpoint.GroupVersion
		}
		return measurements[i].Endpoint.Resource < measurements[j].Endpoint.Resource
	})

	// Detailed results table
	report.WriteString("## Detailed Results\n\n")
	report.WriteString("| Group/Version | Resource | Kind | Scope | Verb | Latency | Response Size | Status | Error |\n")
	report.WriteString("|---------------|----------|------|-------|------|---------|---------------|-----------|-------|\n")

	currentGroup := ""
	for _, m := range measurements {
		// Add group separator for readability
		if m.Endpoint.GroupVersion != currentGroup {
			currentGroup = m.Endpoint.GroupVersion
		}

		scope := "Cluster"
		if m.Endpoint.Namespaced {
			scope = "Namespaced"
		}

		status := "✅ Success"
		errorMsg := "-"
		if !m.Success {
			status = "❌ Failed"
			errorMsg = strings.ReplaceAll(m.Error, "|", "\\|") // Escape pipe characters
			if len(errorMsg) > 50 {
				errorMsg = errorMsg[:47] + "..."
			}
		}

		latencyStr := fmt.Sprintf("%v", m.Latency.Round(time.Microsecond))
		responseSizeStr := formatBytes(m.ResponseSize)

		report.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			m.Endpoint.GroupVersion,
			m.Endpoint.Resource,
			m.Endpoint.Kind,
			scope,
			strings.ToUpper(m.Verb),
			latencyStr,
			responseSizeStr,
			status,
			errorMsg))
	}

	// Top 10 slowest endpoints
	report.WriteString("\n## Top 10 Slowest Endpoints\n\n")

	// Create a copy for sorting by latency
	successfulMeasurements := make([]LatencyMeasurement, 0)
	for _, m := range measurements {
		if m.Success {
			successfulMeasurements = append(successfulMeasurements, m)
		}
	}

	sort.Slice(successfulMeasurements, func(i, j int) bool {
		return successfulMeasurements[i].Latency > successfulMeasurements[j].Latency
	})

	report.WriteString("| Rank | Group/Version | Resource | Latency |\n")
	report.WriteString("|------|---------------|----------|----------|\n")

	for i, m := range successfulMeasurements {
		if i >= 10 {
			break
		}
		report.WriteString(fmt.Sprintf("| %d | %s | %s | %v |\n",
			i+1,
			m.Endpoint.GroupVersion,
			m.Endpoint.Resource,
			m.Latency.Round(time.Microsecond)))
	}

	return report.String()
}
