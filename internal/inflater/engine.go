package inflater

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/kwok"
	"kube-inflater/internal/plan"
	"kube-inflater/internal/resourcegen"
)

// BatchResult captures metrics for a single batch of resource creation.
type BatchResult struct {
	BatchNum int
	Size     int
	Created  int64
	Failed   int64
	Duration time.Duration
}

// Throughput returns the creation rate in resources per second.
func (b BatchResult) Throughput() float64 {
	if b.Duration <= 0 {
		return 0
	}
	return float64(b.Created) / b.Duration.Seconds()
}

// InflationSummary is the overall result of an inflation run.
type InflationSummary struct {
	ResourceType  string
	TotalCreated  int64
	TotalFailed   int64
	TotalDuration time.Duration
	Batches       []BatchResult
}

// Throughput returns the overall creation rate in resources per second.
func (s InflationSummary) Throughput() float64 {
	if s.TotalDuration <= 0 {
		return 0
	}
	return float64(s.TotalCreated) / s.TotalDuration.Seconds()
}

// Engine orchestrates bulk resource creation with a worker pool.
type Engine struct {
	client    kubernetes.Interface
	dynClient dynamic.Interface
	cfg       *cfgpkg.ResourceInflaterConfig
	Results   []InflationSummary
	// setupGens stores generators used for setup-based types (e.g. hollownodes)
	// so that Verify() can access the correct DaemonSet name for label queries.
	setupGens map[string]resourcegen.SetupTeardownGenerator
}

// NewEngine creates a new inflater engine.
func NewEngine(client kubernetes.Interface, dynClient dynamic.Interface, cfg *cfgpkg.ResourceInflaterConfig) *Engine {
	return &Engine{client: client, dynClient: dynClient, cfg: cfg}
}

// Run creates resources for all configured types using exponential batches.
func (e *Engine) Run(ctx context.Context) error {
	for _, typeName := range e.cfg.ResourceTypes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.inflateType(ctx, typeName); err != nil {
			return fmt.Errorf("inflating %s: %w", typeName, err)
		}
	}
	return nil
}

func (e *Engine) inflateType(ctx context.Context, typeName string) error {
	opts := resourcegen.GeneratorOpts{
		DataSizeBytes: e.cfg.DataSizeBytes,
		HollowNode:    e.cfg.HollowNodeOpts,
	}
	gen, err := resourcegen.NewGeneratorWithOpts(typeName, opts)
	if err != nil {
		return err
	}

	// Setup-based generators (e.g. hollownodes) use a different path
	if setupGen, ok := gen.(resourcegen.SetupTeardownGenerator); ok && setupGen.IsSetupBased() {
		return e.inflateSetupType(ctx, setupGen, typeName)
	}

	// For CRDs, create the CRD definition first
	if typeName == "customresources" {
		if err := e.ensureCRD(ctx, gen.(*resourcegen.CRDGenerator)); err != nil {
			return fmt.Errorf("ensuring CRD: %w", err)
		}
	}

	batches := plan.CalculateBatchesPlan(e.cfg.BatchInitial, e.cfg.BatchFactor, e.cfg.CountPerType, e.cfg.MaxBatches)
	logInfo(fmt.Sprintf("Inflating %s: %d total in %d batches (workers=%d)", typeName, e.cfg.CountPerType, len(batches), e.cfg.Workers))

	var summary InflationSummary
	summary.ResourceType = typeName
	runStart := time.Now()

	globalIndex := 0
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return err
		}
		batchNum, batchSize := batch[0], batch[1]
		logInfo(fmt.Sprintf("  Batch %d: creating %d %s", batchNum, batchSize, typeName))

		batchStart := time.Now()
		created, failed, batchErr := e.createBatch(ctx, gen, globalIndex, batchSize)
		batchDuration := time.Since(batchStart)

		br := BatchResult{
			BatchNum: batchNum,
			Size:     batchSize,
			Created:  created,
			Failed:   failed,
			Duration: batchDuration,
		}
		summary.Batches = append(summary.Batches, br)
		summary.TotalCreated += created
		summary.TotalFailed += failed

		logInfo(fmt.Sprintf("  Batch %d done: %d created, %d failed in %v (%.0f/sec)",
			batchNum, created, failed, batchDuration.Round(time.Millisecond), br.Throughput()))

		if batchErr != nil {
			return fmt.Errorf("batch %d: %w", batchNum, batchErr)
		}
		globalIndex += batchSize

		if batchNum < len(batches) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(e.cfg.BatchPause):
			}
		}
	}

	summary.TotalDuration = time.Since(runStart)
	e.Results = append(e.Results, summary)
	logInfo(fmt.Sprintf("Completed inflating %s: %d created in %v (%.0f/sec)",
		typeName, summary.TotalCreated, summary.TotalDuration.Round(time.Millisecond), summary.Throughput()))
	return nil
}

// inflateSetupType handles generators that use one-time setup (e.g., DaemonSet-based hollow nodes).
func (e *Engine) inflateSetupType(ctx context.Context, gen resourcegen.SetupTeardownGenerator, typeName string) error {
	logInfo(fmt.Sprintf("Inflating %s via setup-based generator (count=%d)", typeName, e.cfg.CountPerType))
	runStart := time.Now()

	created, err := gen.Setup(ctx, e.client, e.dynClient, e.cfg.RunID, e.cfg.CountPerType, e.cfg.DryRun)
	if err != nil {
		return fmt.Errorf("setup for %s: %w", typeName, err)
	}

	// Store the generator so Verify() can access it (e.g. for DaemonSet name)
	if e.setupGens == nil {
		e.setupGens = make(map[string]resourcegen.SetupTeardownGenerator)
	}
	e.setupGens[typeName] = gen

	if !e.cfg.DryRun {
		waitTimeout := e.cfg.HollowNodeWaitTimeout
		if waitTimeout <= 0 {
			waitTimeout = 50 * time.Minute
		}
		if err := gen.WaitForReady(ctx, e.client, waitTimeout); err != nil {
			return fmt.Errorf("waiting for %s: %w", typeName, err)
		}
	}

	summary := InflationSummary{
		ResourceType:  typeName,
		TotalCreated:  int64(created),
		TotalDuration: time.Since(runStart),
	}
	e.Results = append(e.Results, summary)
	logInfo(fmt.Sprintf("Completed inflating %s: %d in %v", typeName, created, summary.TotalDuration.Round(time.Millisecond)))
	return nil
}

func (e *Engine) createBatch(ctx context.Context, gen resourcegen.ResourceGenerator, startIndex, count int) (created int64, failed int64, err error) {
	var createdAtomic, failedAtomic atomic.Int64
	var wg sync.WaitGroup

	work := make(chan int, count)
	for i := 0; i < count; i++ {
		work <- startIndex + i
	}
	close(work)

	workers := e.cfg.Workers
	if workers > count {
		workers = count
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				if ctx.Err() != nil {
					return
				}
				if err := e.createOne(ctx, gen, idx); err != nil {
					failedAtomic.Add(1)
					logWarn(fmt.Sprintf("Failed creating %s index %d: %v", gen.TypeName(), idx, err))
				} else {
					createdAtomic.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	return createdAtomic.Load(), failedAtomic.Load(), nil
}

func (e *Engine) createOne(ctx context.Context, gen resourcegen.ResourceGenerator, index int) error {
	ns := ""
	if gen.IsNamespaced() {
		ns = e.namespaceForIndex(index)
	}

	obj, err := gen.Generate(e.cfg.RunID, ns, index)
	if err != nil {
		return err
	}

	if e.cfg.DryRun {
		logInfo(fmt.Sprintf("  [DRY-RUN] Would create %s %s/%s", gen.Kind(), ns, obj.GetName()))
		return nil
	}

	gvr := gen.GVR()
	var res dynamic.ResourceInterface
	if gen.IsNamespaced() {
		res = e.dynClient.Resource(gvr).Namespace(ns)
	} else {
		res = e.dynClient.Resource(gvr)
	}

	_, err = res.Create(ctx, obj, metav1.CreateOptions{})
	return err
}

func (e *Engine) namespaceForIndex(index int) string {
	nsIdx := index % e.cfg.SpreadNamespaces
	return fmt.Sprintf("%s-%d", e.cfg.Namespace, nsIdx)
}

// EnsureKWOK provisions the KWOK controller and fake nodes when pod-bearing
// resource types (pods, jobs, statefulsets) are selected.
func (e *Engine) EnsureKWOK(ctx context.Context) error {
	if !e.cfg.HasPodBearingTypes() {
		return nil
	}

	// Count pod-bearing types to calculate total pod slots needed
	podBearingCount := 0
	for _, t := range e.cfg.ResourceTypes {
		if t == "pods" || t == "jobs" || t == "statefulsets" {
			podBearingCount++
		}
	}

	// Auto-calculate nodes needed: count * number-of-pod-bearing-types
	totalPods := e.cfg.CountPerType * podBearingCount
	needed := kwok.NodesNeeded(totalPods)
	if e.cfg.KWOKNodes < needed {
		logInfo(fmt.Sprintf("Auto-scaling KWOK nodes from %d to %d (need %d pod slots for %d pods)",
			e.cfg.KWOKNodes, needed, totalPods, totalPods))
		e.cfg.KWOKNodes = needed
	}

	prov := kwok.NewProvisioner(e.client, e.dynClient, e.cfg.RunID, e.cfg.DryRun)
	if err := prov.EnsureController(ctx); err != nil {
		return fmt.Errorf("KWOK controller: %w", err)
	}
	if err := prov.CreateFakeNodes(ctx, e.cfg.KWOKNodes); err != nil {
		return fmt.Errorf("KWOK nodes: %w", err)
	}
	return nil
}

// EnsureNamespaces creates the spread namespaces needed for namespaced resources.
func (e *Engine) EnsureNamespaces(ctx context.Context) error {
	hasNamespaced := false
	for _, t := range e.cfg.ResourceTypes {
		if t != "namespaces" {
			gen, err := resourcegen.NewGenerator(t, 0)
			if err != nil {
				continue
			}
			if gen.IsNamespaced() {
				hasNamespaced = true
				break
			}
		}
	}
	if !hasNamespaced {
		return nil
	}

	for i := 0; i < e.cfg.SpreadNamespaces; i++ {
		nsName := fmt.Sprintf("%s-%d", e.cfg.Namespace, i)

		if e.cfg.DryRun {
			logInfo(fmt.Sprintf("[DRY-RUN] Would create namespace %s", nsName))
			continue
		}

		// Wait for terminating namespaces to finish deletion
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) {
			existing, err := e.client.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
			if err != nil {
				break // Doesn't exist, we can create it
			}
			if existing.Status.Phase != "Terminating" {
				break // Exists but not terminating, reuse it
			}
			logInfo(fmt.Sprintf("Namespace %s is terminating, waiting...", nsName))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}

		ns := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]interface{}{
					"name": nsName,
					"labels": resourcegen.ToUnstructuredLabels(map[string]string{
						"app":                         resourcegen.AppLabel,
						resourcegen.RunIDLabel:        e.cfg.RunID,
						resourcegen.ResourceTypeLabel: "spread-namespace",
					}),
				},
			},
		}

		_, err := e.dynClient.Resource(nsGVR).Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			// Might already exist from a previous run
			_, getErr := e.client.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("creating namespace %s: %w", nsName, err)
			}
		}
	}
	return nil
}

func (e *Engine) ensureCRD(ctx context.Context, gen *resourcegen.CRDGenerator) error {
	crd := gen.GenerateCRD(e.cfg.RunID)

	if e.cfg.DryRun {
		logInfo("[DRY-RUN] Would create CRD " + crd.GetName())
		return nil
	}

	_, err := e.dynClient.Resource(gen.CRDGVR()).Create(ctx, crd, metav1.CreateOptions{})
	if err != nil {
		// Check if it already exists
		_, getErr := e.dynClient.Resource(gen.CRDGVR()).Get(ctx, crd.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return err
		}
		logInfo("CRD already exists, reusing")
	} else {
		logInfo("Created CRD " + crd.GetName())
		// Wait for CRD to become established
		time.Sleep(3 * time.Second)
	}
	return nil
}

// Verify checks that the resources reported as created actually exist in the cluster.
// It lists resources by run-id label and compares against the engine's inflation results.
func (e *Engine) Verify(ctx context.Context) error {
	if e.cfg.DryRun {
		logInfo("[DRY-RUN] Skipping verification")
		return nil
	}

	logInfo("🔍 Verifying created resources...")
	labelSelector := fmt.Sprintf("%s=%s", resourcegen.RunIDLabel, e.cfg.RunID)
	allPassed := true

	for _, result := range e.Results {
		opts := resourcegen.GeneratorOpts{
			DataSizeBytes: e.cfg.DataSizeBytes,
			HollowNode:    e.cfg.HollowNodeOpts,
		}
		gen, err := resourcegen.NewGeneratorWithOpts(result.ResourceType, opts)
		if err != nil {
			logWarn(fmt.Sprintf("  ✗ %s: cannot create generator: %v", result.ResourceType, err))
			allPassed = false
			continue
		}

		// Setup-based generators (hollownodes) — count nodes by DaemonSet run-id label
		if _, ok := gen.(resourcegen.SetupTeardownGenerator); ok {
			// Use the stored generator from inflation (has the correct DaemonSet name)
			verifySelector := labelSelector
			if storedGen, found := e.setupGens[result.ResourceType]; found {
				if hn, ok := storedGen.(*resourcegen.HollowNodeGenerator); ok && hn.DaemonSetName() != "" {
					verifySelector = fmt.Sprintf("kubemark=true,%s=%s", resourcegen.RunIDLabel, hn.DaemonSetName())
				}
			}
			actual, err := e.countClusterResources(ctx, gen.GVR(), verifySelector)
			if err != nil {
				logWarn(fmt.Sprintf("  ✗ %s: verification failed: %v", result.ResourceType, err))
				allPassed = false
				continue
			}
			if actual == result.TotalCreated {
				logInfo(fmt.Sprintf("  ✓ %s: %d expected, %d found", result.ResourceType, result.TotalCreated, actual))
			} else {
				logWarn(fmt.Sprintf("  ✗ %s: %d expected, %d found", result.ResourceType, result.TotalCreated, actual))
				allPassed = false
			}
			continue
		}

		var actual int64
		if gen.IsNamespaced() {
			for i := 0; i < e.cfg.SpreadNamespaces; i++ {
				ns := fmt.Sprintf("%s-%d", e.cfg.Namespace, i)
				count, err := e.countNamespacedResources(ctx, gen.GVR(), ns, labelSelector)
				if err != nil {
					logWarn(fmt.Sprintf("  ✗ %s: error listing in %s: %v", result.ResourceType, ns, err))
					allPassed = false
					break
				}
				actual += count
			}
		} else {
			count, err := e.countClusterResources(ctx, gen.GVR(), labelSelector)
			if err != nil {
				logWarn(fmt.Sprintf("  ✗ %s: verification failed: %v", result.ResourceType, err))
				allPassed = false
				continue
			}
			actual = count
		}

		if actual == result.TotalCreated {
			logInfo(fmt.Sprintf("  ✓ %s: %d expected, %d found", result.ResourceType, result.TotalCreated, actual))
		} else {
			logWarn(fmt.Sprintf("  ✗ %s: %d expected, %d found (diff: %+d)", result.ResourceType, result.TotalCreated, actual, actual-result.TotalCreated))
			allPassed = false
		}
	}

	if allPassed {
		logInfo("🔍 Verification passed — all resources confirmed")
	} else {
		logWarn("🔍 Verification found mismatches (see above)")
		return fmt.Errorf("verification failed: some resource counts do not match")
	}
	return nil
}

func (e *Engine) countNamespacedResources(ctx context.Context, gvr schema.GroupVersionResource, namespace, labelSelector string) (int64, error) {
	var count int64
	continueToken := ""
	for {
		list, err := e.dynClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
			Limit:         500,
			Continue:      continueToken,
		})
		if err != nil {
			return 0, err
		}
		count += int64(len(list.Items))
		continueToken = list.GetContinue()
		if continueToken == "" {
			break
		}
	}
	return count, nil
}

func (e *Engine) countClusterResources(ctx context.Context, gvr schema.GroupVersionResource, labelSelector string) (int64, error) {
	var count int64
	continueToken := ""
	for {
		list, err := e.dynClient.Resource(gvr).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
			Limit:         500,
			Continue:      continueToken,
		})
		if err != nil {
			return 0, err
		}
		count += int64(len(list.Items))
		continueToken = list.GetContinue()
		if continueToken == "" {
			break
		}
	}
	return count, nil
}

var nsGVR = (&resourcegen.NamespaceGenerator{}).GVR()

func logInfo(msg string) {
	fmt.Printf("[INFO] %s\n", msg)
}

func logWarn(msg string) {
	fmt.Printf("[WARN] %s\n", msg)
}
