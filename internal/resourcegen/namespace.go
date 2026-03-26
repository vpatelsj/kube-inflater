package resourcegen

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NamespaceGenerator struct{}

func (g *NamespaceGenerator) Generate(runID, _ string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("ns", runID, index)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name":   name,
				"labels": toUnstructuredLabels(CommonLabels(runID, "namespaces")),
			},
		},
	}
	return obj, nil
}

func (g *NamespaceGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
}

func (g *NamespaceGenerator) IsNamespaced() bool { return false }
func (g *NamespaceGenerator) Kind() string       { return "Namespace" }
func (g *NamespaceGenerator) TypeName() string   { return "namespaces" }
