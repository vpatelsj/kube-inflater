package inflater

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/resourcegen"
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

	labelSelector := fmt.Sprintf("app=%s", resourcegen.AppLabel)
	if c.cfg.RunID != "" {
		labelSelector += fmt.Sprintf(",%s=%s", resourcegen.RunIDLabel, c.cfg.RunID)
	}

	// Delete namespaced resources by deleting the spread namespaces (cascading)
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
