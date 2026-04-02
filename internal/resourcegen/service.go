package resourcegen

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ServiceGenerator struct{}

func (g *ServiceGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("svc", runID, index)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(CommonLabels(runID, "services")),
			},
			"spec": map[string]interface{}{
				"type": "ClusterIP",
				"selector": map[string]interface{}{
					"app": name, // non-existent selector — no real endpoints
				},
				"ports": []interface{}{
					map[string]interface{}{
						"port":       int64(80),
						"targetPort": int64(8080),
						"protocol":   "TCP",
					},
				},
			},
		},
	}
	return obj, nil
}

func (g *ServiceGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
}

func (g *ServiceGenerator) IsNamespaced() bool { return true }
func (g *ServiceGenerator) Kind() string       { return "Service" }
func (g *ServiceGenerator) TypeName() string   { return "services" }
