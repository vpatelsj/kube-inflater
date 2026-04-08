package benchmarkio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReadReport loads and decodes a single JSON report file.
// The concrete type is determined by the "type" field.
func ReadReport(path string) (interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading report: %w", err)
	}

	var meta ReportMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing report metadata: %w", err)
	}

	switch meta.Type {
	case ReportResourceCreation:
		var r ResourceCreationReport
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("parsing resource-creation report: %w", err)
		}
		return &r, nil
	case ReportWatchStress:
		var r WatchStressReport
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("parsing watch-stress report: %w", err)
		}
		return &r, nil
	case ReportAPILatency:
		var r APILatencyReport
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("parsing api-latency report: %w", err)
		}
		return &r, nil
	default:
		return nil, fmt.Errorf("unknown report type: %q", meta.Type)
	}
}

// ReadReportRaw loads a report file and returns JSON bytes.
// For .md files, parses the markdown and serializes to JSON.
func ReadReportRaw(path string) (json.RawMessage, error) {
	if strings.HasSuffix(path, ".md") {
		report, err := ParseMarkdownReport(path)
		if err != nil {
			return nil, fmt.Errorf("parsing markdown report: %w", err)
		}
		data, err := json.Marshal(report)
		if err != nil {
			return nil, fmt.Errorf("serializing parsed report: %w", err)
		}
		return json.RawMessage(data), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading report: %w", err)
	}
	// Validate it's valid JSON
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return raw, nil
}

// ListReports scans a directory for JSON benchmark reports and returns
// lightweight summaries sorted by timestamp (newest first).
func ListReports(dir string) ([]ReportListItem, error) {
	return ListReportsFiltered(dir, "")
}

// ListReportsFiltered scans a directory for JSON and Markdown benchmark reports,
// optionally filtering by report type. Returns items sorted by timestamp (newest first).
func ListReportsFiltered(dir string, filterType ReportType) ([]ReportListItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading report directory: %w", err)
	}

	var items []ReportListItem
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		path := filepath.Join(dir, name)

		if strings.HasSuffix(name, ".json") {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var meta ReportMeta
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}
			if filterType != "" && meta.Type != filterType {
				continue
			}
			item := ReportListItem{
				ID:        reportID(name),
				Type:      meta.Type,
				RunID:     meta.RunID,
				Timestamp: meta.Timestamp,
				Filename:  name,
			}
			// Extract resourceTypes for resource-creation reports
			if meta.Type == ReportResourceCreation {
				var partial struct {
					Config struct {
						ResourceTypes []string `json:"resourceTypes"`
					} `json:"config"`
				}
				if json.Unmarshal(data, &partial) == nil {
					item.ResourceTypes = partial.Config.ResourceTypes
				}
			}
			items = append(items, item)
		} else if strings.HasSuffix(name, ".md") {
			report, err := ParseMarkdownReport(path)
			if err != nil {
				continue
			}
			if filterType != "" && report.Type != filterType {
				continue
			}
			items = append(items, ReportListItem{
				ID:            reportID(name),
				Type:          report.Type,
				RunID:         report.RunID,
				Timestamp:     report.Timestamp,
				Filename:      name,
				ResourceTypes: report.Config.ResourceTypes,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})

	return items, nil
}

// FindReportByID locates a report file by its ID (filename without extension).
// Checks for both .json and .md files.
func FindReportByID(dir, id string) (string, error) {
	// Try JSON first
	jsonPath := filepath.Join(dir, id+".json")
	if _, err := os.Stat(jsonPath); err == nil {
		return jsonPath, nil
	}
	// Try markdown
	mdPath := filepath.Join(dir, id+".md")
	if _, err := os.Stat(mdPath); err == nil {
		return mdPath, nil
	}
	return "", fmt.Errorf("report not found: %s", id)
}

func reportID(filename string) string {
	name := strings.TrimSuffix(filename, ".json")
	name = strings.TrimSuffix(name, ".md")
	return name
}
