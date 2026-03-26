package resourcegen

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

// CommonObjectMeta builds ObjectMeta with standard labels.
func CommonObjectMeta(name, namespace, runID, resourceType string) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{
		Name:   name,
		Labels: CommonLabels(runID, resourceType),
	}
	if namespace != "" {
		meta.Namespace = namespace
	}
	return meta
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
