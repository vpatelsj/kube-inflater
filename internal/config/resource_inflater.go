package config

import "time"

const (
	DefaultResourceCount     = 1000
	DefaultWorkers           = 50
	DefaultQPS               = 100
	DefaultBurst             = 200
	DefaultBatchInitial      = 10
	DefaultBatchFactor       = 2
	DefaultMaxBatches        = 25
	DefaultSpreadNamespaces  = 10
	DefaultDataSizeBytes     = 1024
	DefaultBatchPause        = 2 * time.Second
	DefaultWatchConnections  = 100
	DefaultWatchDuration     = 60 * time.Second
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

	// KWOK — schedule pods/jobs/statefulsets on fake KWOK nodes to avoid real kubelet load
	KWOKEnabled bool
	KWOKNodes   int  // number of KWOK fake nodes to create
	KWOKCleanup bool // also remove the KWOK controller on cleanup

	// Watch stress
	WatchEnabled     bool
	WatchOnly        bool
	WatchConnections int
	WatchDuration    time.Duration
	WatchTypes       []string // resource types to watch; defaults to ResourceTypes
}
