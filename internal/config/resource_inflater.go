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
	DefaultKWOKNodes         = 100
)

// HollowNodeOpts configures the hollow node generator.
type HollowNodeOpts struct {
	ContainersPerPod  int
	KubemarkImage     string
	TokenAudiences    []string
	NodeStatusFreq    string
	NodeLeaseDuration int
	NodeMonitorGrace  string
	Namespace         string // hollow-node namespace (default: kubemark-incremental-test)
	WaitTimeout       time.Duration
	PrunePrevious     bool
	RetainDaemonSets  int
	DaemonSetName     string // explicit DaemonSet name; auto-generated if empty
}

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
	CleanupAll       bool
	PerfTests        int
	SkipPerfTests    bool

	// KWOK — automatically enabled when pod-bearing resource types (pods, jobs, statefulsets) are selected.
	KWOKNodes   int  // number of KWOK fake nodes to create (auto-scaled if too low)
	KWOKCleanup bool // also remove the KWOK controller on cleanup

	// Hollow node options — used when resource-types includes "hollownodes"
	HollowNodeOpts        *HollowNodeOpts
	HollowNodeWaitTimeout time.Duration

	// Watch stress options — set by preset or explicit flags
	WatchConnections int
	WatchDuration    time.Duration
	WatchStagger     time.Duration
	WatchTypes       []string
	WatchAgentImage  string
	MutatorRate      int
	MutatorDuration  time.Duration
	MutatorBatchSize int
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

// HasWatchPhase returns true if watch stress parameters are configured.
func (c *ResourceInflaterConfig) HasWatchPhase() bool {
	return c.WatchConnections > 0
}
