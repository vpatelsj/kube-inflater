package resourcegen

import (
	"encoding/base64"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type SecretGenerator struct {
	DataSizeBytes int
}

func (g *SecretGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("secret", runID, index)
	payload := generatePayload(g.DataSizeBytes)
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(CommonLabels(runID, "secrets")),
			},
			"type": "Opaque",
			"data": map[string]interface{}{
				"payload": encoded,
			},
		},
	}
	return obj, nil
}

func (g *SecretGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
}

func (g *SecretGenerator) IsNamespaced() bool { return true }
func (g *SecretGenerator) Kind() string       { return "Secret" }
func (g *SecretGenerator) TypeName() string   { return "secrets" }
