package resourcegen

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type PodGenerator struct{}

func (g *PodGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("pod", runID, index)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(CommonLabels(runID, "pods")),
			},
			"spec": g.podSpec(),
		},
	}
	return obj, nil
}

func (g *PodGenerator) podSpec() map[string]interface{} {
	spec := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"name":  "pause",
				"image": "registry.k8s.io/pause:3.9",
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"cpu":    "1m",
						"memory": "4Mi",
					},
					"limits": map[string]interface{}{
						"cpu":    "1m",
						"memory": "4Mi",
					},
				},
			},
		},
		"terminationGracePeriodSeconds": int64(0),
		"nodeSelector":                  KWOKNodeSelector(),
		"tolerations":                   KWOKTolerations(),
	}
	return spec
}

func (g *PodGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
}

func (g *PodGenerator) IsNamespaced() bool { return true }
func (g *PodGenerator) Kind() string       { return "Pod" }
func (g *PodGenerator) TypeName() string   { return "pods" }
