package resourcegen

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type StatefulSetGenerator struct{}

func (g *StatefulSetGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("sts", runID, index)
	labels := CommonLabels(runID, "statefulsets")
	selectorLabels := map[string]interface{}{
		"app":      name,
		RunIDLabel: runID,
	}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(labels),
			},
			"spec": g.stsSpec(name, selectorLabels),
		},
	}
	return obj, nil
}

func (g *StatefulSetGenerator) stsSpec(name string, selectorLabels map[string]interface{}) map[string]interface{} {
	podSpec := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"name":  "pause",
				"image": "registry.k8s.io/pause:3.9",
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
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
	return map[string]interface{}{
		"replicas":    int64(1),
		"serviceName": name,
		"selector": map[string]interface{}{
			"matchLabels": selectorLabels,
		},
		"template": map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": selectorLabels,
			},
			"spec": podSpec,
		},
	}
}

func (g *StatefulSetGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
}

func (g *StatefulSetGenerator) IsNamespaced() bool { return true }
func (g *StatefulSetGenerator) Kind() string       { return "StatefulSet" }
func (g *StatefulSetGenerator) TypeName() string   { return "statefulsets" }
