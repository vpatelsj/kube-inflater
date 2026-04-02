package resourcegen

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type JobGenerator struct {
	UseKWOK bool
}

func (g *JobGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("job", runID, index)
	labels := CommonLabels(runID, "jobs")
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "batch/v1",
			"kind":       "Job",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(labels),
			},
			"spec": map[string]interface{}{
				"completions":             int64(1),
				"parallelism":             int64(1),
				"backoffLimit":            int64(0),
				"ttlSecondsAfterFinished": int64(300),
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": toUnstructuredLabels(labels),
					},
					"spec": g.podSpec(),
				},
			},
		},
	}
	return obj, nil
}

func (g *JobGenerator) podSpec() map[string]interface{} {
	spec := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"name":    "sleep",
				"image":   "registry.k8s.io/pause:3.9",
				"command": []interface{}{"sleep", "3600"},
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
		"restartPolicy":                 "Never",
		"terminationGracePeriodSeconds": int64(0),
	}
	if g.UseKWOK {
		spec["nodeSelector"] = KWOKNodeSelector()
		spec["tolerations"] = KWOKTolerations()
	}
	return spec
}

func (g *JobGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
}

func (g *JobGenerator) IsNamespaced() bool { return true }
func (g *JobGenerator) Kind() string       { return "Job" }
func (g *JobGenerator) TypeName() string   { return "jobs" }
