package benchmarkio

import "time"

// ReportType identifies the kind of benchmark report.
type ReportType string

const (
	ReportPodCreation ReportType = "pod-creation"
	ReportWatchStress ReportType = "watch-stress"
	ReportAPILatency  ReportType = "api-latency"
)

// ReportMeta contains fields common to all report types.
type ReportMeta struct {
	Type      ReportType `json:"type"`
	RunID     string     `json:"runID"`
	Timestamp time.Time  `json:"timestamp"`
}

// --- Pod Creation Report ---

// PodCreationReport captures a full resource-inflater benchmark run.
type PodCreationReport struct {
	ReportMeta
	Config          PodCreationConfig     `json:"config"`
	Results         []InflationResult     `json:"results"`
	ClusterInfo     *ClusterInfo          `json:"clusterInfo,omitempty"`
	APILatency      []LatencyMeasurement  `json:"apiLatency,omitempty"`
}

// PodCreationConfig holds the parameters used for the run.
type PodCreationConfig struct {
	ResourceTypes    []string `json:"resourceTypes"`
	CountPerType     int      `json:"countPerType"`
	Workers          int      `json:"workers"`
	QPS              float32  `json:"qps"`
	Burst            int      `json:"burst"`
	BatchInitial     int      `json:"batchInitial"`
	BatchFactor      int      `json:"batchFactor"`
	MaxBatches       int      `json:"maxBatches"`
	SpreadNamespaces int      `json:"spreadNamespaces"`
	DataSizeBytes    int      `json:"dataSizeBytes"`
	KWOKNodes        int      `json:"kwokNodes"`
}

// InflationResult captures the outcome of inflating one resource type.
type InflationResult struct {
	ResourceType    string        `json:"resourceType"`
	TotalCreated    int64         `json:"totalCreated"`
	TotalFailed     int64         `json:"totalFailed"`
	TotalDurationMs int64         `json:"totalDurationMs"`
	Throughput      float64       `json:"throughput"`
	Batches         []BatchResult `json:"batches"`
}

// BatchResult captures metrics for one batch.
type BatchResult struct {
	BatchNum    int     `json:"batchNum"`
	Size        int     `json:"size"`
	Created     int64   `json:"created"`
	Failed      int64   `json:"failed"`
	DurationMs  int64   `json:"durationMs"`
	Throughput  float64 `json:"throughput"`
}

// --- Watch Stress Report ---

// WatchStressReport captures a full watch stress benchmark run.
type WatchStressReport struct {
	ReportMeta
	Config          WatchStressConfig   `json:"config"`
	WatchMetrics    WatchMetrics        `json:"watchMetrics"`
	MutatorMetrics  *MutatorMetrics     `json:"mutatorMetrics,omitempty"`
	ScalingData     []ScalingDataPoint  `json:"scalingData,omitempty"`
}

// WatchStressConfig holds the parameters used for the watch stress run.
type WatchStressConfig struct {
	Connections     int    `json:"connections"`
	DurationSec     int    `json:"durationSec"`
	StaggerSec      int    `json:"staggerSec"`
	ResourceTypes   []string `json:"resourceTypes"`
	AgentNodes      int    `json:"agentNodes,omitempty"`
	PodsPerNode     int    `json:"podsPerNode,omitempty"`
	Namespace       string `json:"namespace"`
	SpreadCount     int    `json:"spreadCount"`
}

// WatchMetrics holds aggregated watch performance data.
type WatchMetrics struct {
	TotalEvents         int64   `json:"totalEvents"`
	EventsPerSecond     float64 `json:"eventsPerSecond"`
	Reconnects          int64   `json:"reconnects"`
	Errors              int64   `json:"errors"`
	PeakAliveWatches    int64   `json:"peakAliveWatches"`
	AvgConnectLatencyMs float64 `json:"avgConnectLatencyMs"`
	MaxConnectLatencyMs float64 `json:"maxConnectLatencyMs"`
	AvgDeliveryLatencyMs float64 `json:"avgDeliveryLatencyMs"`
	MaxDeliveryLatencyMs float64 `json:"maxDeliveryLatencyMs"`
	P99DeliveryLatencyMs float64 `json:"p99DeliveryLatencyMs"`
	MinEventsPerConn    int64   `json:"minEventsPerConn"`
	MaxEventsPerConn    int64   `json:"maxEventsPerConn"`
	DurationSec         float64 `json:"durationSec"`
}

// MutatorMetrics holds results from the resource mutator.
type MutatorMetrics struct {
	Creates    int64   `json:"creates"`
	Updates    int64   `json:"updates"`
	Deletes    int64   `json:"deletes"`
	Errors     int64   `json:"errors"`
	ActualRate float64 `json:"actualRate"`
	DurationSec float64 `json:"durationSec"`
}

// ScalingDataPoint captures metrics at a single connection count for scaling analysis.
type ScalingDataPoint struct {
	Connections     int     `json:"connections"`
	EventsPerSec    float64 `json:"eventsPerSec"`
	AvgConnectMs    float64 `json:"avgConnectMs"`
	MaxConnectMs    float64 `json:"maxConnectMs"`
	ErrorRate       float64 `json:"errorRate"`
	PeakAlive       int64   `json:"peakAlive"`
}

// --- API Latency Report ---

// APILatencyReport captures an API performance benchmark run.
type APILatencyReport struct {
	ReportMeta
	ClusterInfo    ClusterInfo          `json:"clusterInfo"`
	Measurements   []LatencyMeasurement `json:"measurements"`
}

// LatencyMeasurement captures a single endpoint latency test.
type LatencyMeasurement struct {
	Endpoint          string `json:"endpoint"`
	GroupVersion      string `json:"groupVersion,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Resource          string `json:"resource,omitempty"`
	Verb              string `json:"verb"`
	LatencyMs         float64 `json:"latencyMs"`
	Success           bool   `json:"success"`
	Error             string `json:"error,omitempty"`
	ResponseCode      int    `json:"responseCode"`
	ResponseSizeBytes int64  `json:"responseSizeBytes"`
}

// --- Shared ---

// ClusterInfo holds a snapshot of cluster state at benchmark time.
type ClusterInfo struct {
	NodeCount             int `json:"nodeCount"`
	PodCount              int `json:"podCount"`
	NamespaceCount        int `json:"namespaceCount"`
	ServiceCount          int `json:"serviceCount"`
	DeploymentCount       int `json:"deploymentCount"`
	ConfigMapCount        int `json:"configMapCount"`
	SecretCount           int `json:"secretCount"`
	PersistentVolumeCount int `json:"persistentVolumeCount"`
	StorageClassCount     int `json:"storageClassCount"`
	IngressCount          int `json:"ingressCount"`
}

// ReportListItem is a lightweight summary used when listing reports.
type ReportListItem struct {
	ID        string     `json:"id"`
	Type      ReportType `json:"type"`
	RunID     string     `json:"runID"`
	Timestamp time.Time  `json:"timestamp"`
	Filename  string     `json:"filename"`
}
