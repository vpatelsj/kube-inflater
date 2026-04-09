package inflater

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/kwok"
	"kube-inflater/internal/nodes"
	"kube-inflater/internal/resourcegen"
	"kube-inflater/internal/watchdeploy"
)

// Cleanup deletes all resources created by a specific run or all runs.
type Cleanup struct {
	client    kubernetes.Interface
	dynClient dynamic.Interface
	cfg       *cfgpkg.ResourceInflaterConfig
}

// NewCleanup creates a new cleanup handler.
func NewCleanup(client kubernetes.Interface, dynClient dynamic.Interface, cfg *cfgpkg.ResourceInflaterConfig) *Cleanup {
	return &Cleanup{client: client, dynClient: dynClient, cfg: cfg}
}

// Run performs cleanup of all resources.
func (c *Cleanup) Run(ctx context.Context) error {
	logInfo("Starting cleanup...")

	// Clean up setup-based generators (e.g. hollownodes)
	for _, typeName := range c.cfg.ResourceTypes {
		opts := resourcegen.GeneratorOpts{
			DataSizeBytes: c.cfg.DataSizeBytes,
			HollowNode:    c.cfg.HollowNodeOpts,
		}
		gen, err := resourcegen.NewGeneratorWithOpts(typeName, opts)
		if err != nil {
			continue
		}
		if setupGen, ok := gen.(resourcegen.SetupTeardownGenerator); ok && setupGen.IsSetupBased() {
			if err := setupGen.Teardown(ctx, c.client, c.dynClient, c.cfg.DryRun); err != nil {
				logWarn(fmt.Sprintf("Error cleaning %s: %v", typeName, err))
			}
		}
	}

	labelSelector := fmt.Sprintf("app=%s", resourcegen.AppLabel)
	if c.cfg.RunID != "" {
		labelSelector += fmt.Sprintf(",%s=%s", resourcegen.RunIDLabel, c.cfg.RunID)
	}

	// Delete namespaced resources directly by label across all spread namespaces.
	// This handles the case where namespaces were created by a prior run and reused.
	if err := c.deleteNamespacedResources(ctx, labelSelector); err != nil {
		logWarn(fmt.Sprintf("Error cleaning namespaced resources: %v", err))
	}

	// Delete spread namespaces that belong to this run (cascading remaining children)
	if err := c.deleteSpreadNamespaces(ctx, labelSelector); err != nil {
		logWarn(fmt.Sprintf("Error cleaning spread namespaces: %v", err))
	}

	// Delete cluster-scoped resources by label
	clusterScopedGVRs := []schema.GroupVersionResource{
		{Group: "", Version: "v1", Resource: "namespaces"},
	}
	for _, gvr := range clusterScopedGVRs {
		if err := c.deleteByLabel(ctx, gvr, labelSelector); err != nil {
			logWarn(fmt.Sprintf("Error cleaning %s: %v", gvr.Resource, err))
		}
	}

	// Delete CRD if present
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	crdName := fmt.Sprintf("%s.%s", resourcegen.CRDPlural, resourcegen.CRDGroup)
	if c.cfg.DryRun {
		logInfo(fmt.Sprintf("[DRY-RUN] Would delete CRD %s", crdName))
	} else {
		err := c.dynClient.Resource(crdGVR).Delete(ctx, crdName, metav1.DeleteOptions{})
		if err != nil {
			logWarn(fmt.Sprintf("CRD deletion (may not exist): %v", err))
		} else {
			logInfo("Deleted CRD " + crdName)
		}
	}

	logInfo("Cleanup completed")
	return nil
}

// deleteNamespacedResources finds all spread namespaces (regardless of run-id)
// and deletes resources matching the label selector within each. This handles
// the common case where namespaces were created by a prior run and reused.
func (c *Cleanup) deleteNamespacedResources(ctx context.Context, labelSelector string) error {
	// Find all spread namespaces by app label (any run-id)
	nsSelector := fmt.Sprintf("app=%s,%s=spread-namespace", resourcegen.AppLabel, resourcegen.ResourceTypeLabel)
	nsList, err := c.dynClient.Resource(nsGVR).List(ctx, metav1.ListOptions{LabelSelector: nsSelector})
	if err != nil {
		return err
	}
	if len(nsList.Items) == 0 {
		return nil
	}

	// Collect namespaced GVRs from configured resource types
	var namespacedGVRs []schema.GroupVersionResource
	for _, typeName := range c.cfg.ResourceTypes {
		gen, err := resourcegen.NewGenerator(typeName, 0)
		if err != nil {
			continue
		}
		if sg, ok := gen.(resourcegen.SetupTeardownGenerator); ok && sg.IsSetupBased() {
			continue
		}
		if gen.IsNamespaced() {
			namespacedGVRs = append(namespacedGVRs, gen.GVR())
		}
	}
	if len(namespacedGVRs) == 0 {
		return nil
	}

	var totalDeleted, totalFailed atomic.Int64
	var wg sync.WaitGroup

	workers := 10
	if workers > len(nsList.Items) {
		workers = len(nsList.Items)
	}

	work := make(chan string, len(nsList.Items))
	for _, ns := range nsList.Items {
		work <- ns.GetName()
	}
	close(work)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for nsName := range work {
				if ctx.Err() != nil {
					return
				}
				for _, gvr := range namespacedGVRs {
					list, err := c.dynClient.Resource(gvr).Namespace(nsName).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
					if err != nil {
						continue
					}
					for _, item := range list.Items {
						if ctx.Err() != nil {
							return
						}
						if c.cfg.DryRun {
							logInfo(fmt.Sprintf("[DRY-RUN] Would delete %s %s/%s", gvr.Resource, nsName, item.GetName()))
							totalDeleted.Add(1)
							continue
						}
						err := c.dynClient.Resource(gvr).Namespace(nsName).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
						if err != nil {
							totalFailed.Add(1)
						} else {
							totalDeleted.Add(1)
						}
					}
				}
			}
		}()
	}
	wg.Wait()

	if d := totalDeleted.Load(); d > 0 || totalFailed.Load() > 0 {
		logInfo(fmt.Sprintf("Namespaced resource cleanup: %d deleted, %d failed", d, totalFailed.Load()))
	}
	return nil
}

func (c *Cleanup) deleteSpreadNamespaces(ctx context.Context, labelSelector string) error {
	spreadLabelSelector := fmt.Sprintf("app=%s,%s=spread-namespace", resourcegen.AppLabel, resourcegen.ResourceTypeLabel)
	if c.cfg.RunID != "" {
		spreadLabelSelector += fmt.Sprintf(",%s=%s", resourcegen.RunIDLabel, c.cfg.RunID)
	}

	list, err := c.dynClient.Resource(nsGVR).List(ctx, metav1.ListOptions{LabelSelector: spreadLabelSelector})
	if err != nil {
		return err
	}

	if len(list.Items) == 0 {
		logInfo("No spread namespaces to clean up")
		return nil
	}

	logInfo(fmt.Sprintf("Deleting %d spread namespaces (cascading all children)...", len(list.Items)))

	var deleted, failed atomic.Int64
	var wg sync.WaitGroup

	workers := 10
	if workers > len(list.Items) {
		workers = len(list.Items)
	}

	work := make(chan string, len(list.Items))
	for _, ns := range list.Items {
		work <- ns.GetName()
	}
	close(work)

	propagation := metav1.DeletePropagationForeground
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for nsName := range work {
				if ctx.Err() != nil {
					return
				}
				if c.cfg.DryRun {
					logInfo(fmt.Sprintf("[DRY-RUN] Would delete namespace %s", nsName))
					deleted.Add(1)
					continue
				}
				err := c.client.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{
					PropagationPolicy: &propagation,
				})
				if err != nil {
					logWarn(fmt.Sprintf("Failed deleting namespace %s: %v", nsName, err))
					failed.Add(1)
				} else {
					deleted.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	logInfo(fmt.Sprintf("Namespace cleanup: %d deleted, %d failed", deleted.Load(), failed.Load()))
	return nil
}

func (c *Cleanup) deleteByLabel(ctx context.Context, gvr schema.GroupVersionResource, labelSelector string) error {
	list, err := c.dynClient.Resource(gvr).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return err
	}

	if len(list.Items) == 0 {
		return nil
	}

	logInfo(fmt.Sprintf("Deleting %d %s by label...", len(list.Items), gvr.Resource))

	for _, item := range list.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.cfg.DryRun {
			logInfo(fmt.Sprintf("[DRY-RUN] Would delete %s %s", gvr.Resource, item.GetName()))
			continue
		}
		err := c.dynClient.Resource(gvr).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
		if err != nil {
			logWarn(fmt.Sprintf("Failed deleting %s %s: %v", gvr.Resource, item.GetName(), err))
		}
	}
	return nil
}

// RunAll performs a nuclear cleanup: removes ALL resources from ALL runs,
// all KWOK infrastructure, all hollow nodes, and all spread namespaces.
func (c *Cleanup) RunAll(ctx context.Context) error {
	logInfo("🧹 Starting full cleanup of ALL kube-inflater resources...")
	dryRun := c.cfg.DryRun

	// 1. Delete all hollow node DaemonSets, pods, and supporting resources
	logInfo("Cleaning up hollow node resources...")
	hollowGen := resourcegen.NewHollowNodeGenerator(c.cfg.HollowNodeOpts)
	if err := hollowGen.Teardown(ctx, c.client, c.dynClient, dryRun); err != nil {
		logWarn(fmt.Sprintf("Hollow node cleanup: %v", err))
	}

	// 2. Delete ALL kubemark nodes (any run-id)
	kubemarkNames, _, err := nodes.ListKubemarkNodes(ctx, c.client)
	if err != nil {
		logWarn(fmt.Sprintf("Error listing kubemark nodes: %v", err))
	} else if len(kubemarkNames) > 0 {
		logInfo(fmt.Sprintf("Deleting %d kubemark nodes...", len(kubemarkNames)))
		if dryRun {
			logInfo(fmt.Sprintf("[DRY-RUN] Would delete %d kubemark nodes", len(kubemarkNames)))
		} else {
			deleted := 0
			for _, name := range kubemarkNames {
				_ = c.client.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
				deleted++
				if deleted%100 == 0 || deleted == len(kubemarkNames) {
					logInfo(fmt.Sprintf("  %d/%d kubemark nodes deleted", deleted, len(kubemarkNames)))
				}
			}
		}
	}

	// 3. Delete ALL KWOK fake nodes and controller/stages
	logInfo("Cleaning up KWOK infrastructure...")
	prov := kwok.NewProvisioner(c.client, c.dynClient, "", dryRun)
	if err := prov.Cleanup(ctx, true); err != nil {
		logWarn(fmt.Sprintf("KWOK cleanup: %v", err))
	}

	// 4. Delete all labeled namespaces (spread + any others) with cascading deletion
	appLabel := fmt.Sprintf("app=%s", resourcegen.AppLabel)
	logInfo("Deleting all kube-inflater namespaces...")
	deleted := c.deleteNamespacesByLabel(ctx, appLabel, dryRun)

	// 5. Delete hollow node namespace
	hollowNS := cfgpkg.DefaultNamespace
	if c.cfg.HollowNodeOpts != nil && c.cfg.HollowNodeOpts.Namespace != "" {
		hollowNS = c.cfg.HollowNodeOpts.Namespace
	}
	if !dryRun {
		_ = c.client.CoreV1().Namespaces().Delete(ctx, hollowNS, metav1.DeleteOptions{})
	}

	// 6. Delete watch-agent resources (Jobs, ConfigMaps, RBAC, FlowSchema, namespace)
	logInfo("Cleaning up watch-agent resources...")
	wd := watchdeploy.NewDeployer(c.client, watchdeploy.Config{DryRun: dryRun})
	if err := wd.Cleanup(ctx); err != nil {
		logWarn(fmt.Sprintf("Watch-agent cleanup: %v", err))
	}

	// 7. Delete CRD (StressItem)
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	crdName := fmt.Sprintf("%s.%s", resourcegen.CRDPlural, resourcegen.CRDGroup)
	if dryRun {
		logInfo(fmt.Sprintf("[DRY-RUN] Would delete CRD %s", crdName))
	} else {
		if err := c.dynClient.Resource(crdGVR).Delete(ctx, crdName, metav1.DeleteOptions{}); err == nil {
			logInfo("Deleted CRD " + crdName)
		}
	}

	// 8. Wait for namespaces to finish terminating
	if !dryRun && deleted > 0 {
		c.waitForNamespaceDeletion(ctx, appLabel)
	}

	logInfo("🧹 Full cleanup completed!")
	return nil
}

// deleteNamespacesByLabel deletes non-Terminating namespaces matching the label.
// Returns the number of delete calls issued.
func (c *Cleanup) deleteNamespacesByLabel(ctx context.Context, labelSelector string, dryRun bool) int {
	nsList, err := c.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		logWarn(fmt.Sprintf("Error listing namespaces: %v", err))
		return 0
	}

	// Filter out already-Terminating namespaces
	var toDelete []string
	terminating := 0
	for _, ns := range nsList.Items {
		if ns.Status.Phase == "Terminating" {
			terminating++
			continue
		}
		toDelete = append(toDelete, ns.Name)
	}

	if terminating > 0 {
		logInfo(fmt.Sprintf("Skipping %d namespaces already terminating", terminating))
	}
	if len(toDelete) == 0 {
		if terminating > 0 {
			logInfo("Waiting for terminating namespaces to finish...")
		}
		return terminating // return terminating count so caller knows to wait
	}

	logInfo(fmt.Sprintf("Deleting %d namespaces...", len(toDelete)))
	propagation := metav1.DeletePropagationForeground
	for _, name := range toDelete {
		if dryRun {
			logInfo(fmt.Sprintf("[DRY-RUN] Would delete namespace %s", name))
			continue
		}
		_ = c.client.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{
			PropagationPolicy: &propagation,
		})
	}
	return len(toDelete) + terminating
}

// waitForNamespaceDeletion polls until all namespaces with the label are gone.
func (c *Cleanup) waitForNamespaceDeletion(ctx context.Context, labelSelector string) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		nsList, err := c.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil || len(nsList.Items) == 0 {
			logInfo("All namespaces deleted")
			return
		}
		logInfo(fmt.Sprintf("Waiting for %d namespaces to terminate...", len(nsList.Items)))
		time.Sleep(3 * time.Second)
	}
	logWarn("Timed out waiting for namespace deletion (namespaces may still be terminating)")
}
