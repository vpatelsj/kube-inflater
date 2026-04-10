package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"kube-inflater/internal/benchmarkio"
	"kube-inflater/internal/clustermon"
	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/runstore"
)

// Run tracks a benchmark run launched from the UI.
type Run struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"` // pending, running, completed, failed
	StartedAt string    `json:"startedAt"`
	EndedAt   string    `json:"endedAt,omitempty"`
	Config    RunConfig `json:"config"`
	LogLines  []string  `json:"logLines,omitempty"`
	ReportID  string    `json:"reportID,omitempty"`
	Error     string    `json:"error,omitempty"`
	CLIRunID  string    `json:"cliRunID,omitempty"` // run-id from the CLI tool (for cluster queries)

	mu        sync.Mutex
	cmd       *exec.Cmd
	startTime time.Time
	snapshots []*clustermon.Snapshot // historical cluster snapshots for chart replay
}

// RunConfig is the user-submitted configuration for a new run.
type RunConfig struct {
	// Pod creation / resource inflater
	Preset           string `json:"preset,omitempty"` // small, medium, large
	ResourceTypes    string `json:"resourceTypes,omitempty"`
	Count            int    `json:"count,omitempty"`
	Workers          int    `json:"workers,omitempty"`
	QPS              int    `json:"qps,omitempty"`
	Burst            int    `json:"burst,omitempty"`
	SpreadNamespaces int    `json:"spreadNamespaces,omitempty"`
	DryRun           bool   `json:"dryRun,omitempty"`

	// API latency
	OnlyCommon bool `json:"onlyCommon,omitempty"`
	NoLimits   bool `json:"noLimits,omitempty"`

	// Watch stress
	Connections  int    `json:"connections,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	Stagger      int    `json:"stagger,omitempty"`
	ResourceType string `json:"resourceType,omitempty"`
}

// RunManager tracks all active and completed runs.
type RunManager struct {
	mu         sync.RWMutex
	runs       map[string]*Run
	reportsDir string
	binDir     string
	store      *runstore.Store // nil if cluster unavailable
}

func NewRunManager(reportsDir, binDir string, store *runstore.Store) *RunManager {
	rm := &RunManager{
		runs:       make(map[string]*Run),
		reportsDir: reportsDir,
		binDir:     binDir,
		store:      store,
	}

	// Restore persisted runs from ConfigMaps
	if store != nil {
		persisted, err := store.LoadAll()
		if err != nil {
			log.Printf("WARNING: Failed to load persisted runs: %v", err)
		} else {
			for _, pr := range persisted {
				run := &Run{
					ID:        pr.ID,
					Type:      pr.Type,
					Status:    pr.Status,
					StartedAt: pr.StartedAt,
					EndedAt:   pr.EndedAt,
					ReportID:  pr.ReportID,
					Error:     pr.Error,
					CLIRunID:  pr.CLIRunID,
					LogLines:  pr.LogLines,
				}
				// Unmarshal config
				if len(pr.Config) > 0 {
					json.Unmarshal(pr.Config, &run.Config)
				}
				// Mark running/pending runs as interrupted (process is gone after restart)
				if run.Status == "running" || run.Status == "pending" {
					run.Status = "failed"
					run.Error = "interrupted by server restart"
					run.EndedAt = time.Now().UTC().Format(time.RFC3339)
					run.LogLines = append(run.LogLines, "❌ Run interrupted by server restart")
					rm.persistRun(run)
				}
				if t, err := time.Parse(time.RFC3339, run.StartedAt); err == nil {
					run.startTime = t
				}
				rm.runs[run.ID] = run
			}
			log.Printf("Restored %d persisted runs from cluster", len(persisted))
		}
	}

	return rm
}

func (rm *RunManager) StartRun(runType string, cfg RunConfig) (*Run, error) {
	id := fmt.Sprintf("run-%s-%04d", time.Now().UTC().Format("20060102-150405"), rand.Intn(10000))

	run := &Run{
		ID:        id,
		Type:      runType,
		Status:    "pending",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Config:    cfg,
		startTime: time.Now(),
	}

	rm.mu.Lock()
	rm.runs[id] = run
	rm.mu.Unlock()

	rm.persistRun(run)

	go rm.executeRun(run)
	return run, nil
}

func (rm *RunManager) GetRun(id string) *Run {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.runs[id]
}

func (rm *RunManager) ListRuns() []*Run {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	result := make([]*Run, 0, len(rm.runs))
	for _, r := range rm.runs {
		result = append(result, r)
	}
	return result
}

// DeleteRun removes a single non-running run from memory and the store.
func (rm *RunManager) DeleteRun(id string) bool {
	rm.mu.Lock()
	run, ok := rm.runs[id]
	if !ok {
		rm.mu.Unlock()
		return false
	}
	run.mu.Lock()
	if run.Status == "running" || run.Status == "pending" {
		run.mu.Unlock()
		rm.mu.Unlock()
		return false
	}
	run.mu.Unlock()
	delete(rm.runs, id)
	rm.mu.Unlock()

	if rm.store != nil {
		rm.store.Delete(id)
	}
	return true
}

// DeleteAllRuns removes all completed/failed runs from memory and the store.
// Returns the number of runs deleted.
func (rm *RunManager) DeleteAllRuns() int {
	rm.mu.Lock()
	var toDelete []string
	for id, run := range rm.runs {
		run.mu.Lock()
		active := run.Status == "running" || run.Status == "pending"
		run.mu.Unlock()
		if !active {
			toDelete = append(toDelete, id)
		}
	}
	for _, id := range toDelete {
		delete(rm.runs, id)
	}
	rm.mu.Unlock()

	if rm.store != nil {
		rm.store.DeleteAll()
	}
	return len(toDelete)
}

func (rm *RunManager) executeRun(run *Run) {
	run.mu.Lock()
	run.Status = "running"
	run.mu.Unlock()
	rm.persistRun(run)

	var args []string
	var binary string

	switch run.Type {
	case "resource-creation":
		binary = filepath.Join(rm.binDir, "kube-inflater")
		args = rm.buildResourceInflaterArgs(run.Config)
	case "api-latency":
		binary = filepath.Join(rm.binDir, "perf-report")
		args = rm.buildPerfReportArgs(run.Config)
	case "watch-stress":
		binary = filepath.Join(rm.binDir, "watch-agent")
		args = rm.buildWatchAgentArgs(run.Config)
	case "cleanup":
		binary = filepath.Join(rm.binDir, "kube-inflater")
		args = []string{"--cleanup-all"}
	default:
		rm.failRun(run, fmt.Sprintf("unknown run type: %s", run.Type))
		return
	}

	// Check binary exists
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		rm.failRun(run, fmt.Sprintf("binary not found: %s — build it first with mage", binary))
		return
	}

	rm.appendLog(run, fmt.Sprintf("$ %s %s", filepath.Base(binary), strings.Join(args, " ")))

	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Dir(rm.binDir)                     // repo root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group — survives parent signals

	run.mu.Lock()
	run.cmd = cmd
	run.mu.Unlock()

	// Capture stdout+stderr combined
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		rm.failRun(run, fmt.Sprintf("failed to create pipe: %v", err))
		return
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		rm.failRun(run, fmt.Sprintf("failed to start: %v", err))
		return
	}

	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	logCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		rm.appendLog(run, line)
		logCount++

		// Persist periodically (every 100 lines) so logs survive crashes
		if logCount%100 == 0 {
			rm.persistRun(run)
		}

		// Extract CLI run-id from log output (e.g. "[INFO] Run ID: 20260406-175709-2843")
		if strings.Contains(line, "Run ID:") {
			parts := strings.SplitN(line, "Run ID:", 2)
			if len(parts) == 2 {
				run.mu.Lock()
				run.CLIRunID = strings.TrimSpace(parts[1])
				run.mu.Unlock()
				rm.persistRun(run)
			}
		}
	}

	err = cmd.Wait()

	run.mu.Lock()
	run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
		rm.appendLogLocked(run, fmt.Sprintf("❌ Process exited with error: %v", err))
	} else {
		run.Status = "completed"
		rm.appendLogLocked(run, "✅ Run completed successfully")
		if run.Type != "cleanup" {
			// Find the report that was just created
			run.ReportID = rm.findLatestReport(run.Type)
		}
	}
	run.mu.Unlock()
	rm.persistRun(run)

	// After successful cleanup, clear all other finished runs
	if run.Type == "cleanup" && err == nil {
		rm.appendLog(run, "🧹 Clearing finished run records...")
		rm.mu.Lock()
		var toDelete []string
		for id, r := range rm.runs {
			if id == run.ID {
				continue
			}
			r.mu.Lock()
			active := r.Status == "running" || r.Status == "pending"
			r.mu.Unlock()
			if !active {
				toDelete = append(toDelete, id)
			}
		}
		for _, id := range toDelete {
			delete(rm.runs, id)
		}
		rm.mu.Unlock()
		if rm.store != nil {
			for _, id := range toDelete {
				rm.store.Delete(id)
			}
		}
		rm.appendLog(run, fmt.Sprintf("🧹 Cleared %d run records", len(toDelete)))
		rm.persistRun(run)
	}
}

func (rm *RunManager) buildResourceInflaterArgs(cfg RunConfig) []string {
	args := []string{
		"--json-report",
		"--report-output-dir", rm.reportsDir,
	}
	if cfg.Preset != "" {
		args = append(args, "--preset", cfg.Preset)
	}
	if cfg.ResourceTypes != "" {
		args = append(args, "--resource-types", cfg.ResourceTypes)
	}
	if cfg.Count > 0 {
		args = append(args, "--count", fmt.Sprintf("%d", cfg.Count))
	}
	if cfg.Workers > 0 {
		args = append(args, "--workers", fmt.Sprintf("%d", cfg.Workers))
	}
	if cfg.QPS > 0 {
		args = append(args, "--qps", fmt.Sprintf("%d", cfg.QPS))
	}
	if cfg.Burst > 0 {
		args = append(args, "--burst", fmt.Sprintf("%d", cfg.Burst))
	}
	if cfg.SpreadNamespaces > 0 {
		args = append(args, "--spread-namespaces", fmt.Sprintf("%d", cfg.SpreadNamespaces))
	}
	if cfg.DryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func (rm *RunManager) buildPerfReportArgs(cfg RunConfig) []string {
	args := []string{
		"--json",
		"--output-dir", rm.reportsDir,
	}
	if cfg.OnlyCommon {
		args = append(args, "--only-common")
	}
	if cfg.NoLimits {
		args = append(args, "--no-limits")
	}
	return args
}

func (rm *RunManager) buildWatchAgentArgs(cfg RunConfig) []string {
	args := []string{
		"watch",
		"--json-report-dir", rm.reportsDir,
	}
	if cfg.Connections > 0 {
		args = append(args, "--connections", fmt.Sprintf("%d", cfg.Connections))
	}
	if cfg.Duration > 0 {
		args = append(args, "--duration", fmt.Sprintf("%d", cfg.Duration))
	}
	if cfg.Stagger > 0 {
		args = append(args, "--stagger", fmt.Sprintf("%d", cfg.Stagger))
	}
	if cfg.ResourceType != "" {
		args = append(args, "--resource-type", cfg.ResourceType)
	}
	return args
}

func (rm *RunManager) failRun(run *Run, msg string) {
	run.mu.Lock()
	run.Status = "failed"
	run.Error = msg
	run.EndedAt = time.Now().UTC().Format(time.RFC3339)
	rm.appendLogLocked(run, "❌ "+msg)
	run.mu.Unlock()
	rm.persistRun(run)
}

// persistRun saves the run state to a ConfigMap (best-effort, non-blocking).
func (rm *RunManager) persistRun(run *Run) {
	if rm.store == nil {
		return
	}
	run.mu.Lock()
	cfgJSON, _ := json.Marshal(run.Config)
	pr := &runstore.PersistedRun{
		ID:        run.ID,
		Type:      run.Type,
		Status:    run.Status,
		StartedAt: run.StartedAt,
		EndedAt:   run.EndedAt,
		Config:    cfgJSON,
		ReportID:  run.ReportID,
		Error:     run.Error,
		CLIRunID:  run.CLIRunID,
		LogLines:  run.LogLines,
	}
	run.mu.Unlock()

	go func() {
		if err := rm.store.Save(pr); err != nil {
			log.Printf("WARNING: failed to persist run %s: %v", pr.ID, err)
		}
	}()
}

func (rm *RunManager) appendLog(run *Run, line string) {
	run.mu.Lock()
	defer run.mu.Unlock()
	rm.appendLogLocked(run, line)
}

func (rm *RunManager) appendLogLocked(run *Run, line string) {
	// Cap log at 10000 lines
	if len(run.LogLines) < 10000 {
		run.LogLines = append(run.LogLines, line)
	}
}

func (rm *RunManager) findLatestReport(runType string) string {
	reportType := benchmarkio.ReportType(runType)
	items, err := benchmarkio.ListReportsFiltered(rm.reportsDir, reportType)
	if err != nil || len(items) == 0 {
		return ""
	}
	return items[0].ID // sorted newest first
}

// --- HTTP Handlers ---

func main() {
	reportsDir := flag.String("reports-dir", "./benchmark-reports", "Primary directory for benchmark reports")
	extraReportsDir := flag.String("extra-reports-dir", "/tmp/benchmark-reports", "Additional directory to scan for reports (e.g. CLI-generated markdown reports)")
	port := flag.Int("port", 8080, "HTTP server port")
	staticDir := flag.String("static-dir", "./ui/dist", "Directory containing the frontend build")
	binDir := flag.String("bin-dir", "./bin", "Directory containing CLI binaries")
	flag.Parse()

	if _, err := os.Stat(*reportsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(*reportsDir, 0o755); err != nil {
			log.Fatalf("Failed creating reports directory: %v", err)
		}
	}

	// Collect all report directories
	reportsDirs := []string{*reportsDir}
	if *extraReportsDir != "" {
		if _, err := os.Stat(*extraReportsDir); err == nil {
			reportsDirs = append(reportsDirs, *extraReportsDir)
		}
	}

	// Initialize run store (persists runs as ConfigMaps — optional)
	var store *runstore.Store
	if s, err := runstore.New(); err != nil {
		log.Printf("WARNING: Run store unavailable (no kubeconfig): %v", err)
		log.Printf("  Runs will not survive server restarts.")
	} else {
		store = s
		log.Printf("Run store initialized — runs will be persisted to cluster")
	}

	runMgr := NewRunManager(*reportsDir, *binDir, store)

	// Initialize cluster monitor (optional — works without a cluster)
	var monitor *clustermon.Monitor
	mon, err := clustermon.New()
	if err != nil {
		log.Printf("WARNING: Cluster monitor unavailable (no kubeconfig): %v", err)
		log.Printf("  Live cluster metrics will not be available. Runs will still work.")
	} else {
		monitor = mon
		log.Printf("Cluster monitor initialized — live metrics available")
	}

	mux := http.NewServeMux()

	// Report API
	mux.HandleFunc("/api/reports", corsMiddleware(handleListReports(reportsDirs)))
	mux.HandleFunc("/api/reports/", corsMiddleware(handleGetReport(reportsDirs)))

	// Presets API
	mux.HandleFunc("/api/presets", corsMiddleware(handlePresets()))

	// Run management API
	mux.HandleFunc("/api/runs", corsMiddleware(handleRuns(runMgr)))
	mux.HandleFunc("/api/runs/", corsMiddleware(handleGetRun(runMgr)))
	mux.HandleFunc("/api/runs/events/", corsMiddleware(handleRunSSE(runMgr)))

	// Live cluster monitoring SSE
	mux.HandleFunc("/api/cluster/live/", corsMiddleware(handleClusterLiveSSE(runMgr, monitor)))

	// Standalone cluster snapshot SSE (no run required)
	mux.HandleFunc("/api/cluster/overview", corsMiddleware(handleClusterOverviewSSE(monitor)))

	// Serve frontend static files
	if _, err := os.Stat(*staticDir); err == nil {
		fs := http.FileServer(spaFileSystem{root: http.Dir(*staticDir)})
		mux.Handle("/", fs)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><html><body>
					<h1>Benchmark UI</h1>
					<p>Frontend not built. Run <code>cd ui && npm run build</code> or access the API at <code>/api/reports</code>.</p>
				</body></html>`)
				return
			}
			http.NotFound(w, r)
		})
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("benchmark-ui serving on http://localhost%s (reports: %v, bins: %s)", addr, reportsDirs, absPath(*binDir))
	log.Fatal(http.ListenAndServe(addr, mux))
}

func handleRuns(rm *RunManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// List all runs
			w.Header().Set("Content-Type", "application/json")
			runs := rm.ListRuns()
			// Strip log lines from list view for performance
			type runSummary struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				Status    string `json:"status"`
				StartedAt string `json:"startedAt"`
				EndedAt   string `json:"endedAt,omitempty"`
				ReportID  string `json:"reportID,omitempty"`
				Error     string `json:"error,omitempty"`
			}
			summaries := make([]runSummary, len(runs))
			for i, run := range runs {
				run.mu.Lock()
				summaries[i] = runSummary{
					ID: run.ID, Type: run.Type, Status: run.Status,
					StartedAt: run.StartedAt, EndedAt: run.EndedAt,
					ReportID: run.ReportID, Error: run.Error,
				}
				run.mu.Unlock()
			}
			json.NewEncoder(w).Encode(summaries)

		case http.MethodPost:
			// Start a new run
			var req struct {
				Type   string    `json:"type"`
				Config RunConfig `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			if req.Type == "" {
				http.Error(w, "type is required (resource-creation, api-latency, watch-stress)", http.StatusBadRequest)
				return
			}

			run, err := rm.StartRun(req.Type, req.Config)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"id": run.ID})

		case http.MethodDelete:
			// Delete all completed/failed runs
			deleted := rm.DeleteAllRuns()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]int{"deleted": deleted})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleGetRun(rm *RunManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
		// Skip sub-paths like /api/runs/events/...
		if strings.Contains(id, "/") {
			return
		}
		if id == "" {
			http.Error(w, "run id required", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			run := rm.GetRun(id)
			if run == nil {
				http.Error(w, "run not found", http.StatusNotFound)
				return
			}

			run.mu.Lock()
			defer run.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(run)

		case http.MethodDelete:
			if !rm.DeleteRun(id) {
				http.Error(w, "run not found or still active", http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"deleted": id})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// handleRunSSE streams run log lines as Server-Sent Events.
func handleRunSSE(rm *RunManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/runs/events/")
		if id == "" {
			http.Error(w, "run id required", http.StatusBadRequest)
			return
		}

		run := rm.GetRun(id)
		if run == nil {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		lastSent := 0
		for {
			run.mu.Lock()
			status := run.Status
			lines := run.LogLines
			reportID := run.ReportID
			run.mu.Unlock()

			// Send any new log lines
			for i := lastSent; i < len(lines); i++ {
				fmt.Fprintf(w, "data: %s\n\n", lines[i])
			}
			lastSent = len(lines)

			// Send status update
			statusJSON, _ := json.Marshal(map[string]string{
				"status":   status,
				"reportID": reportID,
			})
			fmt.Fprintf(w, "event: status\ndata: %s\n\n", statusJSON)
			flusher.Flush()

			if status == "completed" || status == "failed" {
				return
			}

			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

// handleClusterLiveSSE streams live cluster snapshots as SSE for a running benchmark.
func handleClusterLiveSSE(rm *RunManager, monitor *clustermon.Monitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := strings.TrimPrefix(r.URL.Path, "/api/cluster/live/")
		if runID == "" {
			http.Error(w, "run id required", http.StatusBadRequest)
			return
		}

		if monitor == nil {
			http.Error(w, "cluster monitor not available (no kubeconfig)", http.StatusServiceUnavailable)
			return
		}

		run := rm.GetRun(runID)
		if run == nil {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Replay historical snapshots so chart doesn't start from scratch
		run.mu.Lock()
		history := make([]*clustermon.Snapshot, len(run.snapshots))
		copy(history, run.snapshots)
		run.mu.Unlock()
		for _, snap := range history {
			snapJSON, _ := json.Marshal(snap)
			fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapJSON)
		}
		flusher.Flush()

		pollInterval := 2 * time.Second
		ctx := r.Context()

		for {
			run.mu.Lock()
			status := run.Status
			cliRunID := run.CLIRunID
			startTime := run.startTime
			run.mu.Unlock()

			// Take a snapshot from the cluster
			snap, err := monitor.TakeSnapshot(ctx, cliRunID, startTime)
			if ctx.Err() != nil {
				return // client disconnected
			}
			if err != nil {
				errJSON, _ := json.Marshal(map[string]string{"error": err.Error()})
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", errJSON)
			} else {
				// Store snapshot for replay on reconnect
				run.mu.Lock()
				run.snapshots = append(run.snapshots, snap)
				run.mu.Unlock()
				snapJSON, _ := json.Marshal(snap)
				fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapJSON)
			}
			flusher.Flush()

			if status == "completed" || status == "failed" {
				// Send one final snapshot then close
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}
}

// handleClusterOverviewSSE streams cluster-wide snapshots as SSE without requiring a run.
func handleClusterOverviewSSE(monitor *clustermon.Monitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			http.Error(w, "cluster monitor not available (no kubeconfig)", http.StatusServiceUnavailable)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		pollInterval := 2 * time.Second
		ctx := r.Context()
		startTime := time.Now()

		for {
			snap, err := monitor.TakeSnapshot(ctx, "", startTime)
			if ctx.Err() != nil {
				return // client disconnected
			}
			if err != nil {
				errJSON, _ := json.Marshal(map[string]string{"error": err.Error()})
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", errJSON)
			} else {
				snapJSON, _ := json.Marshal(snap)
				fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapJSON)
			}
			flusher.Flush()

			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}
}

func handlePresets() http.HandlerFunc {
	type presetInfo struct {
		Name             string   `json:"name"`
		Label            string   `json:"label"`
		ResourceTypes    []string `json:"resourceTypes"`
		CountPerType     int      `json:"countPerType"`
		Workers          int      `json:"workers"`
		QPS              float32  `json:"qps"`
		Burst            int      `json:"burst"`
		SpreadNamespaces int      `json:"spreadNamespaces"`
		WatchConnections int      `json:"watchConnections"`
		MutatorRate      int      `json:"mutatorRate"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		result := make([]presetInfo, 0, len(cfgpkg.PresetNames))
		for _, name := range cfgpkg.PresetNames {
			p := cfgpkg.Presets[name]
			result = append(result, presetInfo{
				Name:             p.Name,
				Label:            p.Label,
				ResourceTypes:    p.ResourceTypes,
				CountPerType:     p.CountPerType,
				Workers:          p.Workers,
				QPS:              p.QPS,
				Burst:            p.Burst,
				SpreadNamespaces: p.SpreadNamespaces,
				WatchConnections: p.WatchConnections,
				MutatorRate:      p.MutatorRate,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleListReports(dirs []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		filterType := benchmarkio.ReportType(r.URL.Query().Get("type"))
		var allItems []benchmarkio.ReportListItem
		seen := make(map[string]bool)
		for _, dir := range dirs {
			items, err := benchmarkio.ListReportsFiltered(dir, filterType)
			if err != nil {
				continue
			}
			for _, item := range items {
				if !seen[item.ID] {
					// Tag the item with the directory so we can find it later
					item.Filename = filepath.Join(dir, filepath.Base(item.Filename))
					seen[item.ID] = true
					allItems = append(allItems, item)
				}
			}
		}

		// Sort newest first
		sort.Slice(allItems, func(i, j int) bool {
			return allItems[i].Timestamp.After(allItems[j].Timestamp)
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(allItems)
	}
}

func handleGetReport(dirs []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		id := r.URL.Path[len("/api/reports/"):]
		if id == "" {
			http.Error(w, "report id required", http.StatusBadRequest)
			return
		}

		// Search all directories for the report
		var foundPath string
		for _, dir := range dirs {
			path, err := benchmarkio.FindReportByID(dir, id)
			if err == nil {
				foundPath = path
				break
			}
		}
		if foundPath == "" {
			http.Error(w, "report not found: "+id, http.StatusNotFound)
			return
		}

		raw, err := benchmarkio.ReadReportRaw(foundPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

type spaFileSystem struct {
	root http.FileSystem
}

func (s spaFileSystem) Open(name string) (http.File, error) {
	f, err := s.root.Open(name)
	if os.IsNotExist(err) {
		return s.root.Open("index.html")
	}
	return f, err
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
