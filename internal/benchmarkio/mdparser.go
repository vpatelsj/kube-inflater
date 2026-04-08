package benchmarkio

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseMarkdownReport reads a markdown benchmark report file and converts it
// to a ResourceCreationReport. Returns nil if the file cannot be parsed.
func ParseMarkdownReport(path string) (*ResourceCreationReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	report := &ResourceCreationReport{}
	report.Type = ReportResourceCreation

	// Parse header metadata
	for _, line := range lines {
		if strings.HasPrefix(line, "**Generated:**") {
			ts := strings.TrimPrefix(line, "**Generated:**")
			ts = strings.TrimSpace(ts)
			if t, err := time.Parse("2006-01-02 15:04:05 MST", ts); err == nil {
				report.Timestamp = t
			}
		}
		if strings.HasPrefix(line, "**Run ID:**") {
			id := strings.TrimPrefix(line, "**Run ID:**")
			id = strings.TrimSpace(id)
			id = strings.Trim(id, "`")
			report.RunID = id
		}
	}

	// Parse sections by heading
	sections := parseSections(lines)

	// Run Configuration
	if kv := parseKVTable(sections["Run Configuration"]); kv != nil {
		report.Config = parseRunConfig(kv)
	}

	// Cluster Information
	if kv := parseKVTable(sections["Cluster Information"]); kv != nil {
		ci := parseClusterInfo(kv)
		report.ClusterInfo = &ci
	}

	// Pod Creation Throughput
	for heading, sectionLines := range sections {
		if strings.HasPrefix(heading, "Pod Creation Throughput") {
			kv := parseKVTable(sectionLines)
			resourceType := "pods"
			if idx := strings.Index(heading, "—"); idx >= 0 {
				resourceType = strings.TrimSpace(heading[idx+len("—"):])
			}

			result := parseInflationResult(kv, resourceType)

			// Per-Batch Breakdown (subsection)
			if batchLines, ok := sections["Per-Batch Breakdown"]; ok {
				result.Batches = parseBatchTable(batchLines)
			}

			report.Results = append(report.Results, result)
		}
	}

	// API Server Performance
	if apiLines, ok := sections["API Server Performance (under load)"]; ok {
		if kv := parseKVTable(apiLines); kv != nil {
			report.APILatency = parseAPILatency(kv, sections["Top 10 Slowest Endpoints"])
		}
	}

	if report.RunID == "" {
		return nil, fmt.Errorf("could not parse run ID from %s", path)
	}

	return report, nil
}

// parseSections splits markdown lines by ## and ### headings.
func parseSections(lines []string) map[string][]string {
	sections := make(map[string][]string)
	var currentHeading string

	for _, line := range lines {
		if strings.HasPrefix(line, "### ") {
			currentHeading = strings.TrimPrefix(line, "### ")
			currentHeading = strings.TrimSpace(currentHeading)
		} else if strings.HasPrefix(line, "## ") {
			currentHeading = strings.TrimPrefix(line, "## ")
			currentHeading = strings.TrimSpace(currentHeading)
		} else if currentHeading != "" {
			sections[currentHeading] = append(sections[currentHeading], line)
		}
	}
	return sections
}

// parseKVTable extracts key-value pairs from a markdown table with two columns.
func parseKVTable(lines []string) map[string]string {
	kv := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cols := splitTableRow(line)
		if len(cols) >= 2 {
			kv[cols[0]] = cols[1]
		}
	}
	if len(kv) == 0 {
		return nil
	}
	return kv
}

// splitTableRow splits a markdown table row into trimmed columns.
func splitTableRow(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	var cols []string
	for _, p := range parts {
		cols = append(cols, strings.TrimSpace(p))
	}
	return cols
}

func parseRunConfig(kv map[string]string) ResourceCreationConfig {
	return ResourceCreationConfig{
		ResourceTypes:    []string{kv["Resource Types"]},
		CountPerType:     atoi(kv["Count per Type"]),
		Workers:          atoi(kv["Workers"]),
		QPS:              float32(atoi(kv["QPS"])),
		Burst:            atoi(kv["Burst"]),
		SpreadNamespaces: atoi(kv["Spread Namespaces"]),
		KWOKNodes:        atoi(kv["KWOK Nodes"]),
		BatchInitial:     100, // default
		BatchFactor:      2,   // default
	}
}

func parseClusterInfo(kv map[string]string) ClusterInfo {
	return ClusterInfo{
		NodeCount:       atoi(kv["Nodes"]),
		PodCount:        atoi(kv["Pods"]),
		NamespaceCount:  atoi(kv["Namespaces"]),
		ServiceCount:    atoi(kv["Services"]),
		DeploymentCount: atoi(kv["Deployments"]),
		ConfigMapCount:  atoi(kv["ConfigMaps"]),
		SecretCount:     atoi(kv["Secrets"]),
	}
}

func parseInflationResult(kv map[string]string, resourceType string) InflationResult {
	result := InflationResult{
		ResourceType: resourceType,
		TotalCreated: atoi64(kv["Total Created"]),
		TotalFailed:  atoi64(kv["Total Failed"]),
		Throughput:   atof(kv["Overall Throughput"]),
	}
	if dur, ok := kv["Total Duration"]; ok {
		result.TotalDurationMs = parseDurationMs(dur)
	}
	return result
}

func parseBatchTable(lines []string) []BatchResult {
	var batches []BatchResult
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cols := splitTableRow(line)
		if len(cols) < 6 {
			continue
		}
		// Skip header row
		if cols[0] == "Batch" {
			continue
		}
		batches = append(batches, BatchResult{
			BatchNum:   atoi(cols[0]),
			Size:       atoi(cols[1]),
			Created:    atoi64(cols[2]),
			Failed:     atoi64(cols[3]),
			DurationMs: parseDurationMs(cols[4]),
			Throughput: atof(cols[5]),
		})
	}
	return batches
}

func parseAPILatency(kv map[string]string, topEndpoints []string) []LatencyMeasurement {
	var measurements []LatencyMeasurement

	// Parse the top slowest endpoints table
	if topEndpoints == nil {
		return measurements
	}
	for _, line := range topEndpoints {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cols := splitTableRow(line)
		if len(cols) < 4 || cols[0] == "Rank" {
			continue
		}
		gv := cols[1]
		resource := cols[2]
		latency := cols[3]

		measurements = append(measurements, LatencyMeasurement{
			Endpoint:     fmt.Sprintf("/apis/%s/%s", gv, resource),
			GroupVersion: gv,
			Resource:     resource,
			Verb:         "list",
			LatencyMs:    parseDurationMsFloat(latency),
			Success:      true,
			ResponseCode: 200,
		})
	}

	return measurements
}

// parseDurationMs parses human-readable durations like "36m27.378s", "1.295s", "221ms".
func parseDurationMs(s string) int64 {
	return int64(math.Round(parseDurationMsFloat(s)))
}

func parseDurationMsFloat(s string) float64 {
	s = strings.TrimSpace(s)
	// Remove trailing "/sec" from throughput values
	s = strings.TrimSuffix(s, "/sec")

	// Try standard Go duration parsing first
	if d, err := time.ParseDuration(s); err == nil {
		return float64(d.Milliseconds())
	}

	// Handle "Xm Y.Zs" format (e.g. "36m27.378s")
	re := regexp.MustCompile(`(?:(\d+)m)?(\d+(?:\.\d+)?)(s|ms)`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		return 0
	}

	var totalMs float64
	if matches[1] != "" {
		minutes, _ := strconv.ParseFloat(matches[1], 64)
		totalMs += minutes * 60 * 1000
	}
	val, _ := strconv.ParseFloat(matches[2], 64)
	if matches[3] == "s" {
		totalMs += val * 1000
	} else {
		totalMs += val
	}
	return totalMs
}

func atoi(s string) int {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	v, _ := strconv.Atoi(s)
	return v
}

func atoi64(s string) int64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func atof(s string) float64 {
	s = strings.TrimSpace(s)
	// Remove units like "resources/sec", "/sec", "ms"
	s = strings.TrimSuffix(s, " resources/sec")
	s = strings.TrimSuffix(s, "/sec")
	s = strings.TrimSuffix(s, "ms")
	s = strings.ReplaceAll(s, ",", "")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
