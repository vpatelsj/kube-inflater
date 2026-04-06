package config

import "time"

const (
	DefaultResourceCount    = 1000
	DefaultWorkers          = 50
	DefaultQPS              = 100
	DefaultBurst            = 200
	DefaultBatchInitial     = 10
	DefaultBatchFactor      = 2
	DefaultMaxBatches       = 25
	DefaultSpreadNamespaces = 10
	DefaultDataSizeBytes    = 1024
	DefaultBatchPause       = 2 * time.Second

	DefaultResourceNamespace = "stress-test"
	DefaultKWOKNodes         = 10
)

// ResourceInflaterConfig holds configuration for the kube-resource-inflater command.
type ResourceInflaterConfig struct {
	ResourceTypes    []string
	CountPerType     int
	Workers          int
	QPS              float32
	Burst            int
	BatchInitial     int
	BatchFactor      int
	MaxBatches       int
	SpreadNamespaces int
	DataSizeBytes    int
	BatchPause       time.Duration
	RunID            string
	Namespace        string
	DryRun           bool
	CleanupOnly      bool
	PerfTests        int
	SkipPerfTests    bool

	// KWOK — automatically enabled when pod-bearing resource types (pods, jobs, statefulsets) are selected.
	// Pods always schedule on KWOK fake nodes to avoid real kubelet load.
	KWOKNodes   int  // number of KWOK fake nodes to create (auto-scaled if too low)
	KWOKCleanup bool // also remove the KWOK controller on cleanup
}

// HasPodBearingTypes returns true if any of the configured resource types create pods.
func (c *ResourceInflaterConfig) HasPodBearingTypes() bool {
	for _, t := range c.ResourceTypes {
		if t == "pods" || t == "jobs" || t == "statefulsets" {
			return true
		}
	}
	return false
}
