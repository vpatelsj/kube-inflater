package resourcegen

import "fmt"

// GeneratorOpts are options passed to generator constructors.
type GeneratorOpts struct {
	DataSizeBytes int
	UseKWOK       bool
}

// registry maps short type names to generator constructors.
var registry = map[string]func(opts GeneratorOpts) ResourceGenerator{
	"configmaps":      func(o GeneratorOpts) ResourceGenerator { return &ConfigMapGenerator{DataSizeBytes: o.DataSizeBytes} },
	"secrets":         func(o GeneratorOpts) ResourceGenerator { return &SecretGenerator{DataSizeBytes: o.DataSizeBytes} },
	"services":        func(_ GeneratorOpts) ResourceGenerator { return &ServiceGenerator{} },
	"namespaces":      func(_ GeneratorOpts) ResourceGenerator { return &NamespaceGenerator{} },
	"pods":            func(o GeneratorOpts) ResourceGenerator { return &PodGenerator{UseKWOK: o.UseKWOK} },
	"serviceaccounts": func(_ GeneratorOpts) ResourceGenerator { return &ServiceAccountGenerator{} },
	"jobs":            func(o GeneratorOpts) ResourceGenerator { return &JobGenerator{UseKWOK: o.UseKWOK} },
	"statefulsets":    func(o GeneratorOpts) ResourceGenerator { return &StatefulSetGenerator{UseKWOK: o.UseKWOK} },
	"customresources": func(o GeneratorOpts) ResourceGenerator { return &CRDGenerator{DataSizeBytes: o.DataSizeBytes} },
}

// AllTypeNames returns sorted list of supported resource type names.
func AllTypeNames() []string {
	return []string{
		"configmaps",
		"customresources",
		"jobs",
		"namespaces",
		"pods",
		"secrets",
		"serviceaccounts",
		"services",
		"statefulsets",
	}
}

// NewGenerator creates a ResourceGenerator for the given type name.
// Kept for backward compat — creates with default opts (no KWOK).
func NewGenerator(typeName string, dataSizeBytes int) (ResourceGenerator, error) {
	return NewGeneratorWithOpts(typeName, GeneratorOpts{DataSizeBytes: dataSizeBytes})
}

// NewGeneratorWithOpts creates a ResourceGenerator with full options.
func NewGeneratorWithOpts(typeName string, opts GeneratorOpts) (ResourceGenerator, error) {
	ctor, ok := registry[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown resource type %q; supported: %v", typeName, AllTypeNames())
	}
	return ctor(opts), nil
}
