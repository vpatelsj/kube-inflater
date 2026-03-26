package resourcegen

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ServiceAccountGenerator struct{}

func (g *ServiceAccountGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("sa", runID, index)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ServiceAccount",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(CommonLabels(runID, "serviceaccounts")),
			},
		},
	}
	return obj, nil
}

func (g *ServiceAccountGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
}

func (g *ServiceAccountGenerator) IsNamespaced() bool { return true }
func (g *ServiceAccountGenerator) Kind() string       { return "ServiceAccount" }
func (g *ServiceAccountGenerator) TypeName() string   { return "serviceaccounts" }
