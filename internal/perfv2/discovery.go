package perfv2

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
)

// APIEndpoint represents a discovered API server endpoint
type APIEndpoint struct {
	GroupVersion string
	Kind         string
	Resource     string
	Namespaced   bool
	Verbs        []string
	FullPath     string
}

// EndpointDiscovery handles discovery of API server endpoints
type EndpointDiscovery struct {
	client    kubernetes.Interface
	discovery discovery.DiscoveryInterface
	ctx       context.Context
}

// NewEndpointDiscovery creates a new endpoint discovery instance
func NewEndpointDiscovery(client kubernetes.Interface, ctx context.Context) *EndpointDiscovery {
	return &EndpointDiscovery{
		client:    client,
		discovery: client.Discovery(),
		ctx:       ctx,
	}
}

// DiscoverAllEndpoints discovers all available API server endpoints
func (ed *EndpointDiscovery) DiscoverAllEndpoints() ([]APIEndpoint, error) {
	var endpoints []APIEndpoint

	// Discover server groups
	groups, err := ed.discovery.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("failed to discover server groups: %w", err)
	}

	// Process each group
	for _, group := range groups.Groups {
		for _, version := range group.Versions {
			groupVersion := version.GroupVersion

			// Get resources for this group/version
			resourceList, err := ed.discovery.ServerResourcesForGroupVersion(groupVersion)
			if err != nil {
				// Skip groups that can't be discovered (some may require special permissions)
				continue
			}

			// Process each resource
			for _, resource := range resourceList.APIResources {
				// Skip subresources for now
				if strings.Contains(resource.Name, "/") {
					continue
				}

				endpoint := APIEndpoint{
					GroupVersion: groupVersion,
					Kind:         resource.Kind,
					Resource:     resource.Name,
					Namespaced:   resource.Namespaced,
					Verbs:        resource.Verbs,
					FullPath:     ed.buildFullPath(groupVersion, resource.Name, resource.Namespaced),
				}

				endpoints = append(endpoints, endpoint)
			}
		}
	}

	// Sort endpoints by group version and resource name
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].GroupVersion != endpoints[j].GroupVersion {
			return endpoints[i].GroupVersion < endpoints[j].GroupVersion
		}
		return endpoints[i].Resource < endpoints[j].Resource
	})

	return endpoints, nil
}

// buildFullPath constructs the full API path for a resource
func (ed *EndpointDiscovery) buildFullPath(groupVersion, resource string, namespaced bool) string {
	// Handle core API group (v1)
	if groupVersion == "v1" {
		if namespaced {
			return fmt.Sprintf("/api/v1/namespaces/{namespace}/%s", resource)
		}
		return fmt.Sprintf("/api/v1/%s", resource)
	}

	// Handle other API groups
	parts := strings.Split(groupVersion, "/")
	if len(parts) != 2 {
		return fmt.Sprintf("/apis/%s/%s", groupVersion, resource)
	}

	group, version := parts[0], parts[1]
	if namespaced {
		return fmt.Sprintf("/apis/%s/%s/namespaces/{namespace}/%s", group, version, resource)
	}
	return fmt.Sprintf("/apis/%s/%s/%s", group, version, resource)
}

// FilterEndpointsByVerbs filters endpoints that support specific verbs
func (ed *EndpointDiscovery) FilterEndpointsByVerbs(endpoints []APIEndpoint, verbs ...string) []APIEndpoint {
	var filtered []APIEndpoint

	for _, endpoint := range endpoints {
		hasAllVerbs := true
		for _, requiredVerb := range verbs {
			found := false
			for _, supportedVerb := range endpoint.Verbs {
				if supportedVerb == requiredVerb {
					found = true
					break
				}
			}
			if !found {
				hasAllVerbs = false
				break
			}
		}

		if hasAllVerbs {
			filtered = append(filtered, endpoint)
		}
	}

	return filtered
}

// GetCommonEndpoints returns a curated list of commonly used endpoints for performance testing
func (ed *EndpointDiscovery) GetCommonEndpoints(endpoints []APIEndpoint) []APIEndpoint {
	commonResources := map[string]bool{
		"nodes":                  true,
		"pods":                   true,
		"services":               true,
		"deployments":            true,
		"replicasets":            true,
		"configmaps":             true,
		"secrets":                true,
		"namespaces":             true,
		"persistentvolumes":      true,
		"persistentvolumeclaims": true,
		"serviceaccounts":        true,
		"endpoints":              true,
		"events":                 true,
	}

	var common []APIEndpoint
	for _, endpoint := range endpoints {
		if commonResources[endpoint.Resource] {
			common = append(common, endpoint)
		}
	}

	return common
}

// PrintEndpoints prints discovered endpoints in a formatted way
func (ed *EndpointDiscovery) PrintEndpoints(endpoints []APIEndpoint) {
	fmt.Printf("Discovered %d API endpoints:\n\n", len(endpoints))

	currentGroup := ""
	for _, endpoint := range endpoints {
		if endpoint.GroupVersion != currentGroup {
			fmt.Printf("=== %s ===\n", endpoint.GroupVersion)
			currentGroup = endpoint.GroupVersion
		}

		namespacedStr := "cluster-scoped"
		if endpoint.Namespaced {
			namespacedStr = "namespaced"
		}

		fmt.Printf("  %-20s %-15s %s [%s]\n",
			endpoint.Resource,
			endpoint.Kind,
			namespacedStr,
			strings.Join(endpoint.Verbs, ","))
	}
}
