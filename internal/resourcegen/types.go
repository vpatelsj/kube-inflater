package resourcegen

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	AppLabel          = "kube-resource-inflater"
	RunIDLabel        = "run-id"
	ResourceTypeLabel = "resource-type"
)

// ResourceGenerator generates Kubernetes resources for stress testing.
type ResourceGenerator interface {
	// Generate creates a single resource object for the given run and index.
	// namespace is used for namespaced resources; ignored for cluster-scoped ones.
	Generate(runID string, namespace string, index int) (*unstructured.Unstructured, error)

	// GVR returns the GroupVersionResource for this generator.
	GVR() schema.GroupVersionResource

	// IsNamespaced returns true if this resource type is namespace-scoped.
	IsNamespaced() bool

	// Kind returns the Kubernetes Kind (e.g. "ConfigMap").
	Kind() string

	// TypeName returns the short name used in --resource-types flag (e.g. "configmaps").
	TypeName() string
}

// SetupTeardownGenerator is an optional interface for resource types that need
// one-time setup/teardown (e.g., a DaemonSet that implicitly creates nodes)
// rather than per-item creation via Generate().
type SetupTeardownGenerator interface {
	ResourceGenerator

	// Setup performs one-time resource creation (SA, RBAC, DaemonSet, etc.).
	// count is the total desired resources. Returns the number that will be created.
	Setup(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, runID string, count int, dryRun bool) (int, error)

	// WaitForReady blocks until all implicitly-created resources are ready.
	WaitForReady(ctx context.Context, client kubernetes.Interface, timeout time.Duration) error

	// Teardown removes resources created by Setup.
	Teardown(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, dryRun bool) error

	// IsSetupBased returns true — the engine skips per-item Generate() batching.
	IsSetupBased() bool
}

// CommonLabels returns the standard labels applied to every generated resource.
func CommonLabels(runID, resourceType string) map[string]string {
	return map[string]string{
		"app":             AppLabel,
		RunIDLabel:        runID,
		ResourceTypeLabel: resourceType,
	}
}

// ResourceName generates a deterministic name for a resource.
func ResourceName(prefix, runID string, index int) string {
	return fmt.Sprintf("%s-%s-%d", prefix, runID, index)
}

// KWOKNodeSelector returns the nodeSelector targeting KWOK fake nodes.
func KWOKNodeSelector() map[string]interface{} {
	return map[string]interface{}{
		"type": "kwok",
	}
}

// KWOKTolerations returns tolerations for scheduling on tainted KWOK nodes.
func KWOKTolerations() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"key":      "type",
			"operator": "Equal",
			"value":    "kwok",
			"effect":   "NoSchedule",
		},
	}
}
