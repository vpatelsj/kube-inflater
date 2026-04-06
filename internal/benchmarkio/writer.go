package benchmarkio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kube-inflater/internal/inflater"
	"kube-inflater/internal/perfv2"
	"kube-inflater/internal/watchstress"
)

// WritePodCreationReport converts engine results and optional perf data into a
// PodCreationReport and writes it as JSON to the given directory.
func WritePodCreationReport(dir string, runID string, cfg PodCreationConfig, results []inflater.InflationSummary, clusterInfo *perfv2.ClusterInfo, measurements []perfv2.LatencyMeasurement) (string, error) {
	report := PodCreationReport{
		ReportMeta: ReportMeta{
			Type:      ReportPodCreation,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
		},
		Config:  cfg,
		Results: convertInflationResults(results),
	}

	if clusterInfo != nil {
		report.ClusterInfo = convertClusterInfo(clusterInfo)
	}
	if len(measurements) > 0 {
		report.APILatency = convertLatencyMeasurements(measurements)
	}

	return writeJSON(dir, fmt.Sprintf("pod-creation-%s", runID), &report)
}

// WriteWatchStressReport converts watch metrics into a WatchStressReport JSON file.
func WriteWatchStressReport(dir string, runID string, cfg WatchStressConfig, metrics *watchstress.MetricsSummary, mutator *watchstress.MutatorSummary) (string, error) {
	report := WatchStressReport{
		ReportMeta: ReportMeta{
			Type:      ReportWatchStress,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
		},
		Config:       cfg,
		WatchMetrics: convertWatchMetrics(metrics),
	}

	if mutator != nil {
		report.MutatorMetrics = &MutatorMetrics{
			Creates:     mutator.Creates,
			Updates:     mutator.Updates,
			Deletes:     mutator.Deletes,
			Errors:      mutator.Errors,
			ActualRate:  mutator.ActualRate,
			DurationSec: mutator.Duration,
		}
	}

	return writeJSON(dir, fmt.Sprintf("watch-stress-%s", runID), &report)
}

// WriteAPILatencyReport converts perf reporter data into an APILatencyReport JSON file.
func WriteAPILatencyReport(dir string, runID string, clusterInfo *perfv2.ClusterInfo, measurements []perfv2.LatencyMeasurement) (string, error) {
	report := APILatencyReport{
		ReportMeta: ReportMeta{
			Type:      ReportAPILatency,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
		},
		Measurements: convertLatencyMeasurements(measurements),
	}
	if clusterInfo != nil {
		report.ClusterInfo = *convertClusterInfo(clusterInfo)
	}

	return writeJSON(dir, fmt.Sprintf("api-latency-%s", runID), &report)
}

func convertInflationResults(results []inflater.InflationSummary) []InflationResult {
	out := make([]InflationResult, len(results))
	for i, r := range results {
		batches := make([]BatchResult, len(r.Batches))
		for j, b := range r.Batches {
			batches[j] = BatchResult{
				BatchNum:   b.BatchNum,
				Size:       b.Size,
				Created:    b.Created,
				Failed:     b.Failed,
				DurationMs: b.Duration.Milliseconds(),
				Throughput: b.Throughput(),
			}
		}
		out[i] = InflationResult{
			ResourceType:    r.ResourceType,
			TotalCreated:    r.TotalCreated,
			TotalFailed:     r.TotalFailed,
			TotalDurationMs: r.TotalDuration.Milliseconds(),
			Throughput:      r.Throughput(),
			Batches:         batches,
		}
	}
	return out
}

func convertClusterInfo(ci *perfv2.ClusterInfo) *ClusterInfo {
	return &ClusterInfo{
		NodeCount:             ci.NodeCount,
		PodCount:              ci.PodCount,
		NamespaceCount:        ci.NamespaceCount,
		ServiceCount:          ci.ServiceCount,
		DeploymentCount:       ci.DeploymentCount,
		ConfigMapCount:        ci.ConfigMapCount,
		SecretCount:           ci.SecretCount,
		PersistentVolumeCount: ci.PersistentVolumeCount,
		StorageClassCount:     ci.StorageClassCount,
		IngressCount:          ci.IngressCount,
	}
}

func convertLatencyMeasurements(ms []perfv2.LatencyMeasurement) []LatencyMeasurement {
	out := make([]LatencyMeasurement, len(ms))
	for i, m := range ms {
		out[i] = LatencyMeasurement{
			Endpoint:          m.Endpoint.FullPath,
			GroupVersion:      m.Endpoint.GroupVersion,
			Kind:              m.Endpoint.Kind,
			Resource:          m.Endpoint.Resource,
			Verb:              m.Verb,
			LatencyMs:         float64(m.Latency.Milliseconds()),
			Success:           m.Success,
			Error:             m.Error,
			ResponseCode:      m.ResponseCode,
			ResponseSizeBytes: m.ResponseSize,
		}
	}
	return out
}

func convertWatchMetrics(m *watchstress.MetricsSummary) WatchMetrics {
	return WatchMetrics{
		TotalEvents:          m.TotalEvents,
		EventsPerSecond:      m.EventsPerSecond,
		Reconnects:           m.Reconnects,
		Errors:               m.Errors,
		PeakAliveWatches:     m.PeakAliveWatches,
		AvgConnectLatencyMs:  float64(m.AvgConnectLatency.Milliseconds()),
		MaxConnectLatencyMs:  float64(m.MaxConnectLatency.Milliseconds()),
		AvgDeliveryLatencyMs: float64(m.AvgDeliveryLatency.Milliseconds()),
		MaxDeliveryLatencyMs: float64(m.MaxDeliveryLatency.Milliseconds()),
		P99DeliveryLatencyMs: float64(m.P99DeliveryLatency.Milliseconds()),
		MinEventsPerConn:     m.MinEventsPerConn,
		MaxEventsPerConn:     m.MaxEventsPerConn,
		DurationSec:          m.Duration.Seconds(),
	}
}

func writeJSON(dir, prefix string, v interface{}) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating report directory: %w", err)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s.json", prefix, ts)
	path := filepath.Join(dir, filename)

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling report: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing report: %w", err)
	}

	return path, nil
}
