package config

import "time"

// Preset defines a named t-shirt-size configuration for resource inflation.
type Preset struct {
	Name             string
	Label            string // human-readable label
	ResourceTypes    []string
	CountPerType     int
	Workers          int
	QPS              float32
	Burst            int
	BatchInitial     int
	BatchFactor      int
	MaxBatches       int
	SpreadNamespaces int
	BatchPause       time.Duration

	// Watch stress parameters (zero values = skip watch phase)
	WatchConnections int
	WatchStagger     time.Duration
	WatchTypes       []string // resource types to watch (defaults to ["customresources"])
	MutatorRate      int      // mutations per second
	MutatorBatchSize int
}

// Presets maps short names to preset configurations.
var Presets = map[string]Preset{
	"small": {
		Name:             "small",
		Label:            "Small (10k per type)",
		ResourceTypes:    []string{"configmaps", "secrets", "pods", "jobs", "statefulsets", "services", "serviceaccounts", "customresources", "hollownodes"},
		CountPerType:     10_000,
		Workers:          50,
		QPS:              100,
		Burst:            200,
		BatchInitial:     10,
		BatchFactor:      2,
		MaxBatches:       25,
		SpreadNamespaces: 10,
		BatchPause:       2 * time.Second,

		WatchConnections: 10_000,
		WatchStagger:     30 * time.Second,
		WatchTypes:       []string{"customresources"},
		MutatorRate:      500,
		MutatorBatchSize: 50,
	},
	"medium": {
		Name:             "medium",
		Label:            "Medium (50k per type)",
		ResourceTypes:    []string{"configmaps", "secrets", "pods", "jobs", "statefulsets", "services", "serviceaccounts", "customresources", "hollownodes"},
		CountPerType:     50_000,
		Workers:          100,
		QPS:              300,
		Burst:            500,
		BatchInitial:     50,
		BatchFactor:      2,
		MaxBatches:       30,
		SpreadNamespaces: 50,
		BatchPause:       2 * time.Second,

		WatchConnections: 50_000,
		WatchStagger:     60 * time.Second,
		WatchTypes:       []string{"customresources"},
		MutatorRate:      2000,
		MutatorBatchSize: 100,
	},
	"large": {
		Name:             "large",
		Label:            "Large (100k per type)",
		ResourceTypes:    []string{"configmaps", "secrets", "pods", "jobs", "statefulsets", "services", "serviceaccounts", "customresources", "hollownodes"},
		CountPerType:     100_000,
		Workers:          200,
		QPS:              500,
		Burst:            1000,
		BatchInitial:     100,
		BatchFactor:      2,
		MaxBatches:       30,
		SpreadNamespaces: 100,
		BatchPause:       2 * time.Second,

		WatchConnections: 100_000,
		WatchStagger:     120 * time.Second,
		WatchTypes:       []string{"customresources"},
		MutatorRate:      5000,
		MutatorBatchSize: 200,
	},
}

// PresetNames returns valid preset names in order.
var PresetNames = []string{"small", "medium", "large"}

// ApplyPreset sets fields on the config from a named preset.
// Returns false if the preset name is unknown.
func (c *ResourceInflaterConfig) ApplyPreset(name string) bool {
	p, ok := Presets[name]
	if !ok {
		return false
	}
	c.ResourceTypes = p.ResourceTypes
	c.CountPerType = p.CountPerType
	c.Workers = p.Workers
	c.QPS = p.QPS
	c.Burst = p.Burst
	c.BatchInitial = p.BatchInitial
	c.BatchFactor = p.BatchFactor
	c.MaxBatches = p.MaxBatches
	c.SpreadNamespaces = p.SpreadNamespaces
	c.BatchPause = p.BatchPause

	c.WatchConnections = p.WatchConnections
	c.WatchStagger = p.WatchStagger
	c.WatchTypes = p.WatchTypes
	c.MutatorRate = p.MutatorRate
	c.MutatorBatchSize = p.MutatorBatchSize
	return true
}
