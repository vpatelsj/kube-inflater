package perfv2

import (
	"fmt"
	"io"
	"strings"
	"time"

	"kube-inflater/internal/inflater"
)

// BenchmarkReport combines resource creation metrics with API performance data.
type BenchmarkReport struct {
	// Run configuration
	RunID            string
	ResourceTypes    []string
	CountPerType     int
	Workers          int
	QPS              float32
	Burst            int
	SpreadNamespaces int
	KWOKNodes        int

	// Results
	InflationResults []inflater.InflationSummary
	ClusterInfo      *ClusterInfo
	APIMeasurements  []LatencyMeasurement
}

// RenderMarkdown writes a team-shareable markdown benchmark report.
func (r *BenchmarkReport) RenderMarkdown(w io.Writer) {
	timestamp := time.Now().Format("2006-01-02 15:04:05 UTC")

	p := func(format string, args ...interface{}) {
		fmt.Fprintf(w, format+"\n", args...)
	}

	p("# Pod Benchmark Report")
	p("")
	p("**Generated:** %s  ", timestamp)
	p("**Run ID:** `%s`  ", r.RunID)
	p("")

	// Run configuration
	p("## Run Configuration")
	p("")
	p("| Parameter | Value |")
	p("|-----------|-------|")
	p("| Resource Types | %s |", strings.Join(r.ResourceTypes, ", "))
	p("| Count per Type | %d |", r.CountPerType)
	p("| Workers | %d |", r.Workers)
	p("| QPS | %.0f |", r.QPS)
	p("| Burst | %d |", r.Burst)
	p("| Spread Namespaces | %d |", r.SpreadNamespaces)
	p("| KWOK Nodes | %d |", r.KWOKNodes)
	p("| Scheduling | KWOK (fake nodes) |")
	p("")

	// Cluster info
	if r.ClusterInfo != nil {
		p("## Cluster Information")
		p("")
		p("| Resource | Count |")
		p("|----------|-------|")
		p("| Nodes | %d |", r.ClusterInfo.NodeCount)
		p("| Pods | %d |", r.ClusterInfo.PodCount)
		p("| Namespaces | %d |", r.ClusterInfo.NamespaceCount)
		p("| Services | %d |", r.ClusterInfo.ServiceCount)
		p("| Deployments | %d |", r.ClusterInfo.DeploymentCount)
		p("| ConfigMaps | %d |", r.ClusterInfo.ConfigMapCount)
		p("| Secrets | %d |", r.ClusterInfo.SecretCount)
		p("")
	}

	// Pod creation throughput
	for _, summary := range r.InflationResults {
		p("## Pod Creation Throughput — %s", summary.ResourceType)
		p("")
		p("| Metric | Value |")
		p("|--------|-------|")
		p("| Total Created | %d |", summary.TotalCreated)
		p("| Total Failed | %d |", summary.TotalFailed)
		p("| Total Duration | %v |", summary.TotalDuration.Round(time.Millisecond))
		p("| Overall Throughput | %.1f resources/sec |", summary.Throughput())
		p("| Batches | %d |", len(summary.Batches))
		p("")

		// Per-batch breakdown
		p("### Per-Batch Breakdown")
		p("")
		p("| Batch | Size | Created | Failed | Duration | Throughput |")
		p("|-------|------|---------|--------|----------|------------|")
		for _, b := range summary.Batches {
			p("| %d | %d | %d | %d | %v | %.1f/sec |",
				b.BatchNum, b.Size, b.Created, b.Failed,
				b.Duration.Round(time.Millisecond), b.Throughput())
		}
		p("")
	}

	// API performance (if available)
	if len(r.APIMeasurements) > 0 {
		successful := 0
		var totalLatency time.Duration
		var minLatency, maxLatency time.Duration
		first := true

		for _, m := range r.APIMeasurements {
			if m.Success {
				successful++
				totalLatency += m.Latency
				if first || m.Latency < minLatency {
					minLatency = m.Latency
				}
				if m.Latency > maxLatency {
					maxLatency = m.Latency
				}
				first = false
			}
		}

		avgLatency := time.Duration(0)
		if successful > 0 {
			avgLatency = totalLatency / time.Duration(successful)
		}

		p("## API Server Performance (under load)")
		p("")
		p("| Metric | Value |")
		p("|--------|-------|")
		p("| Endpoints Tested | %d |", len(r.APIMeasurements))
		p("| Successful | %d |", successful)
		p("| Failed | %d |", len(r.APIMeasurements)-successful)
		p("| Avg Latency | %v |", avgLatency.Round(time.Microsecond))
		p("| Min Latency | %v |", minLatency.Round(time.Microsecond))
		p("| Max Latency | %v |", maxLatency.Round(time.Microsecond))
		p("")

		// Top 10 slowest
		p("### Top 10 Slowest Endpoints")
		p("")
		p("| Rank | Group/Version | Resource | Latency |")
		p("|------|---------------|----------|---------|")

		// Sort by latency descending (make a copy to avoid mutating)
		sorted := make([]LatencyMeasurement, 0, len(r.APIMeasurements))
		for _, m := range r.APIMeasurements {
			if m.Success {
				sorted = append(sorted, m)
			}
		}
		// Simple selection of top 10 without importing sort
		for rank := 0; rank < 10 && rank < len(sorted); rank++ {
			maxIdx := rank
			for j := rank + 1; j < len(sorted); j++ {
				if sorted[j].Latency > sorted[maxIdx].Latency {
					maxIdx = j
				}
			}
			sorted[rank], sorted[maxIdx] = sorted[maxIdx], sorted[rank]
			m := sorted[rank]
			p("| %d | %s | %s | %v |",
				rank+1, m.Endpoint.GroupVersion, m.Endpoint.Resource,
				m.Latency.Round(time.Microsecond))
		}
		p("")
	}

	// Summary
	p("## Summary")
	p("")
	for _, summary := range r.InflationResults {
		if summary.TotalFailed == 0 {
			p("- **%s**: %d resources created in %v (%.1f/sec) — no failures",
				summary.ResourceType, summary.TotalCreated,
				summary.TotalDuration.Round(time.Second), summary.Throughput())
		} else {
			p("- **%s**: %d created, %d failed in %v (%.1f/sec)",
				summary.ResourceType, summary.TotalCreated, summary.TotalFailed,
				summary.TotalDuration.Round(time.Second), summary.Throughput())
		}
	}
	p("")
}
