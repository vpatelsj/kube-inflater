package resourcegen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ConfigMapGenerator struct {
	DataSizeBytes int
}

func (g *ConfigMapGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("cm", runID, index)
	data := generatePayload(g.DataSizeBytes)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(CommonLabels(runID, "configmaps")),
			},
			"data": map[string]interface{}{
				"payload": data,
			},
		},
	}
	return obj, nil
}

func (g *ConfigMapGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
}

func (g *ConfigMapGenerator) IsNamespaced() bool { return true }
func (g *ConfigMapGenerator) Kind() string       { return "ConfigMap" }
func (g *ConfigMapGenerator) TypeName() string   { return "configmaps" }

func generatePayload(size int) string {
	if size <= 0 {
		size = 1024
	}
	// Generate random hex data (each byte becomes 2 hex chars)
	raw := make([]byte, (size+1)/2)
	if _, err := rand.Read(raw); err != nil {
		// fallback to deterministic padding
		return fmt.Sprintf("%0*d", size, 0)
	}
	s := hex.EncodeToString(raw)
	if len(s) > size {
		s = s[:size]
	}
	return s
}

// ToUnstructuredLabels converts typed labels to unstructured format.
func ToUnstructuredLabels(labels map[string]string) map[string]interface{} {
	m := make(map[string]interface{}, len(labels))
	for k, v := range labels {
		m[k] = v
	}
	return m
}

// alias for internal use
var toUnstructuredLabels = ToUnstructuredLabels
