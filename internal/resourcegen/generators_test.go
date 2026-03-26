package resourcegen

import (
	"strings"
	"testing"
)

func TestAllGeneratorsProduceValidObjects(t *testing.T) {
	runID := "test-run-001"
	namespace := "test-ns"

	for _, typeName := range AllTypeNames() {
		t.Run(typeName, func(t *testing.T) {
			gen, err := NewGenerator(typeName, 1024)
			if err != nil {
				t.Fatalf("NewGenerator(%q): %v", typeName, err)
			}

			ns := namespace
			if !gen.IsNamespaced() {
				ns = ""
			}

			obj, err := gen.Generate(runID, ns, 42)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			// Verify apiVersion and kind are set
			if obj.GetAPIVersion() == "" {
				t.Error("apiVersion is empty")
			}
			if obj.GetKind() == "" {
				t.Error("kind is empty")
			}
			if obj.GetKind() != gen.Kind() {
				t.Errorf("kind mismatch: got %q, want %q", obj.GetKind(), gen.Kind())
			}

			// Verify name is set and contains run ID
			name := obj.GetName()
			if name == "" {
				t.Error("name is empty")
			}
			if !strings.Contains(name, runID) {
				t.Errorf("name %q does not contain run ID %q", name, runID)
			}

			// Verify labels
			labels := obj.GetLabels()
			if labels == nil {
				t.Fatal("labels are nil")
			}
			if labels["app"] != AppLabel {
				t.Errorf("app label: got %q, want %q", labels["app"], AppLabel)
			}
			if labels[RunIDLabel] != runID {
				t.Errorf("run-id label: got %q, want %q", labels[RunIDLabel], runID)
			}
			if labels[ResourceTypeLabel] == "" {
				t.Error("resource-type label is empty")
			}

			// Verify namespace for namespaced resources
			if gen.IsNamespaced() {
				if obj.GetNamespace() != namespace {
					t.Errorf("namespace: got %q, want %q", obj.GetNamespace(), namespace)
				}
			}

			// Verify GVR has resource set
			gvr := gen.GVR()
			if gvr.Resource == "" {
				t.Error("GVR resource is empty")
			}
			if gvr.Version == "" {
				t.Error("GVR version is empty")
			}

			// Verify TypeName matches registry key
			if gen.TypeName() != typeName {
				t.Errorf("TypeName: got %q, want %q", gen.TypeName(), typeName)
			}
		})
	}
}

func TestRegistryAllTypesListed(t *testing.T) {
	names := AllTypeNames()
	if len(names) != len(registry) {
		t.Errorf("AllTypeNames() has %d entries but registry has %d", len(names), len(registry))
	}
	for _, name := range names {
		if _, ok := registry[name]; !ok {
			t.Errorf("type %q listed in AllTypeNames but not in registry", name)
		}
	}
}

func TestNewGeneratorUnknownType(t *testing.T) {
	_, err := NewGenerator("nonexistent", 0)
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestGeneratePayload(t *testing.T) {
	sizes := []int{0, 1, 100, 1024, 10000}
	for _, size := range sizes {
		payload := generatePayload(size)
		expected := size
		if size == 0 {
			expected = 1024
		}
		if len(payload) != expected {
			t.Errorf("generatePayload(%d): got length %d, want %d", size, len(payload), expected)
		}
	}
}

func TestResourceName(t *testing.T) {
	name := ResourceName("cm", "run-123", 42)
	if name != "cm-run-123-42" {
		t.Errorf("ResourceName: got %q, want %q", name, "cm-run-123-42")
	}
}

func TestCRDGenerator(t *testing.T) {
	gen := &CRDGenerator{DataSizeBytes: 512}
	crd := gen.GenerateCRD("test-run")

	if crd.GetKind() != "CustomResourceDefinition" {
		t.Errorf("CRD kind: got %q", crd.GetKind())
	}

	name := crd.GetName()
	if !strings.Contains(name, CRDPlural) || !strings.Contains(name, CRDGroup) {
		t.Errorf("CRD name %q doesn't match expected format", name)
	}

	// Test CR generation
	cr, err := gen.Generate("test-run", "default", 0)
	if err != nil {
		t.Fatal(err)
	}
	if cr.GetKind() != CRDKind {
		t.Errorf("CR kind: got %q, want %q", cr.GetKind(), CRDKind)
	}
}
