package resourcegen

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	CRDGroup   = "stresstest.kube-inflater.io"
	CRDVersion = "v1alpha1"
	CRDKind    = "StressItem"
	CRDPlural  = "stressitems"
)

// CRDGenerator creates both the CRD definition (once) and CR instances.
type CRDGenerator struct {
	DataSizeBytes int
}

// GenerateCRD returns the CustomResourceDefinition object.
// This should be created once before generating CR instances.
func (g *CRDGenerator) GenerateCRD(runID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name":   fmt.Sprintf("%s.%s", CRDPlural, CRDGroup),
				"labels": toUnstructuredLabels(CommonLabels(runID, "customresourcedefinitions")),
			},
			"spec": map[string]interface{}{
				"group": CRDGroup,
				"names": map[string]interface{}{
					"kind":     CRDKind,
					"listKind": CRDKind + "List",
					"plural":   CRDPlural,
					"singular": "stressitem",
				},
				"scope": "Namespaced",
				"versions": []interface{}{
					map[string]interface{}{
						"name":    CRDVersion,
						"served":  true,
						"storage": true,
						"schema": map[string]interface{}{
							"openAPIV3Schema": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"spec": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"payload": map[string]interface{}{
												"type": "string",
											},
											"index": map[string]interface{}{
												"type": "integer",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// Generate creates a CR instance.
func (g *CRDGenerator) Generate(runID, namespace string, index int) (*unstructured.Unstructured, error) {
	name := ResourceName("si", runID, index)
	payload := generatePayload(g.DataSizeBytes)
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": fmt.Sprintf("%s/%s", CRDGroup, CRDVersion),
			"kind":       CRDKind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    toUnstructuredLabels(CommonLabels(runID, "customresources")),
			},
			"spec": map[string]interface{}{
				"payload": payload,
				"index":   int64(index),
			},
		},
	}
	return obj, nil
}

func (g *CRDGenerator) CRDGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
}

func (g *CRDGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: CRDGroup, Version: CRDVersion, Resource: CRDPlural}
}

func (g *CRDGenerator) IsNamespaced() bool { return true }
func (g *CRDGenerator) Kind() string       { return CRDKind }
func (g *CRDGenerator) TypeName() string   { return "customresources" }
