package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/homedir"
)

type CleanupStats struct {
	ZombieNodesDeleted   int
	NotReadyNodesDeleted int
	PendingPodsDeleted   int
	FailedPodsDeleted    int
	OtherPodsDeleted     int
	Errors               []string
}

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	namespace := flag.String("namespace", "kubemark-incremental-test", "namespace to check for hollow node pods")
	dryRun := flag.Bool("dry-run", false, "show what would be deleted without actually deleting")
	zombieOnly := flag.Bool("zombie-only", false, "only delete zombie nodes (nodes without corresponding pods)")
	notreadyOnly := flag.Bool("notready-only", false, "only delete NotReady nodes")
	force := flag.Bool("force", false, "force delete nodes without grace period")
	includePods := flag.Bool("include-pods", false, "also cleanup problematic pods (Pending, Failed, etc.)")
	pendingOnly := flag.Bool("pending-only", false, "only delete Pending pods (requires --include-pods)")
	failedOnly := flag.Bool("failed-only", false, "only delete Failed pods (requires --include-pods)")

	concurrency := flag.Int("concurrency", 10, "number of concurrent deletions (default 10)")
	delayMs := flag.Int("delay", 10, "delay between deletions in milliseconds (default 10)")
	disableRateLimit := flag.Bool("disable-rate-limit", false, "disable client-side rate limiting entirely for maximum speed")
	labelSelector := flag.String("l", "", "delete ALL nodes matching this label selector (e.g. kubemark=true); skips zombie/NotReady classification")

	flag.Parse()

	if *zombieOnly && *notreadyOnly {
		fmt.Fprintf(os.Stderr, "Error: cannot specify both --zombie-only and --notready-only\n")
		os.Exit(1)
	}

	if (*pendingOnly || *failedOnly) && !*includePods {
		fmt.Fprintf(os.Stderr, "Error: --pending-only and --failed-only require --include-pods\n")
		os.Exit(1)
	}

	if *pendingOnly && *failedOnly {
		fmt.Fprintf(os.Stderr, "Error: cannot specify both --pending-only and --failed-only\n")
		os.Exit(1)
	}

	// Build kubernetes config
	var config *rest.Config
	var err error

	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building kubeconfig: %v\n", err)
		os.Exit(1)
	}

	// Bypass client-side throttling for faster operations
	config.QPS = 1000   // Increase queries per second (default is 5)
	config.Burst = 2000 // Increase burst capacity (default is 10)

	// Optional: Disable rate limiting entirely for maximum speed
	if *disableRateLimit {
		config.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating kubernetes client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if *dryRun {
		fmt.Printf("🔍 DRY RUN: Analyzing nodes for cleanup (no actual deletions)\n\n")
	} else {
		fmt.Printf("⚠️  CLEANUP MODE: Will delete nodes from cluster\n")
		if *force {
			fmt.Printf("🚨 FORCE MODE: Nodes will be deleted immediately without grace period\n")
		}
		fmt.Printf("\n")
	}

	// Label-selector mode: use a separate client with HTTP/2 disabled to avoid
	// GOAWAY cascading all in-flight requests when running 100+ concurrent deletes.
	if *labelSelector != "" {
		h1Config := rest.CopyConfig(config)
		h1Config.TLSClientConfig.NextProtos = []string{"http/1.1"}
		// Force HTTP/1.1 connection pool — one TCP connection per concurrent request.
		h1Config.Wrap(func(rt http.RoundTripper) http.RoundTripper {
			if t, ok := rt.(*http.Transport); ok {
				t.MaxIdleConnsPerHost = *concurrency + 10
				t.MaxIdleConns = *concurrency + 10
				t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
			}
			return rt
		})
		h1Client, err := kubernetes.NewForConfig(h1Config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating HTTP/1.1 client: %v\n", err)
			os.Exit(1)
		}
		deleteByLabelSelector(ctx, h1Client, *labelSelector, *concurrency, *delayMs, *force, *dryRun)
		return
	}

	// Get all pods in the namespace to identify which hollow nodes have corresponding pods
	pods, err := clientset.CoreV1().Pods(*namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing pods: %v\n", err)
		os.Exit(1)
	}

	// Get all nodes to check their status
	allNodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing nodes: %v\n", err)
		os.Exit(1)
	}

	// Track which hollow nodes have corresponding pods
	hollowNodesWithPods := make(map[string]bool)
	for _, pod := range pods.Items {
		hollowNodesWithPods[pod.Name] = true
	}

	// Find nodes to delete
	var zombieNodes []string
	var notreadyNodes []string

	for _, node := range allNodes.Items {
		if !strings.HasPrefix(node.Name, "hollow-node") {
			continue // Skip non-hollow nodes
		}

		// Check if node is NotReady
		isNotReady := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status != "True" {
				isNotReady = true
				break
			}
		}

		// Check if this is a zombie node (no corresponding pod)
		hasCorrespondingPod := hollowNodesWithPods[node.Name]

		if !hasCorrespondingPod {
			zombieNodes = append(zombieNodes, node.Name)
		} else if isNotReady {
			notreadyNodes = append(notreadyNodes, node.Name)
		}
	}

	// Analyze pods for cleanup if requested
	var pendingPods []string
	var failedPods []string
	var otherProblematicPods []string

	if *includePods {
		for _, pod := range pods.Items {
			phase := string(pod.Status.Phase)

			switch phase {
			case "Pending":
				// Check if it's been pending for more than a reasonable time
				pendingPods = append(pendingPods, pod.Name)
			case "Failed":
				failedPods = append(failedPods, pod.Name)
			case "Unknown":
				// Include Unknown phase pods as problematic
				otherProblematicPods = append(otherProblematicPods, pod.Name)
			case "Succeeded":
				// Completed pods might need cleanup in some cases
				// For now, we'll leave them alone unless specifically requested
			case "Running":
				// Check if any containers are not ready for a long time
				// This is more complex analysis we could add later
			}
		}
	}

	// Determine what to delete based on flags
	var nodesToDelete []string
	var deleteReasons []string

	if *zombieOnly {
		nodesToDelete = zombieNodes
		for range zombieNodes {
			deleteReasons = append(deleteReasons, "zombie")
		}
	} else if *notreadyOnly {
		nodesToDelete = notreadyNodes
		for range notreadyNodes {
			deleteReasons = append(deleteReasons, "notready")
		}
	} else {
		// Delete both types
		nodesToDelete = append(nodesToDelete, zombieNodes...)
		nodesToDelete = append(nodesToDelete, notreadyNodes...)
		for range zombieNodes {
			deleteReasons = append(deleteReasons, "zombie")
		}
		for range notreadyNodes {
			deleteReasons = append(deleteReasons, "notready")
		}
	}

	// Determine what pods to delete based on flags
	var podsToDelete []string
	var podDeleteReasons []string

	if *includePods {
		if *pendingOnly {
			podsToDelete = pendingPods
			for range pendingPods {
				podDeleteReasons = append(podDeleteReasons, "pending")
			}
		} else if *failedOnly {
			podsToDelete = failedPods
			for range failedPods {
				podDeleteReasons = append(podDeleteReasons, "failed")
			}
		} else {
			// Delete all problematic pods
			podsToDelete = append(podsToDelete, pendingPods...)
			podsToDelete = append(podsToDelete, failedPods...)
			podsToDelete = append(podsToDelete, otherProblematicPods...)
			for range pendingPods {
				podDeleteReasons = append(podDeleteReasons, "pending")
			}
			for range failedPods {
				podDeleteReasons = append(podDeleteReasons, "failed")
			}
			for range otherProblematicPods {
				podDeleteReasons = append(podDeleteReasons, "unknown")
			}
		}
	}

	// Show summary
	fmt.Printf("📊 Cleanup Summary:\n")
	fmt.Printf("Zombie nodes found: %d\n", len(zombieNodes))
	fmt.Printf("NotReady nodes found: %d\n", len(notreadyNodes))
	fmt.Printf("Total nodes to delete: %d\n", len(nodesToDelete))

	if *includePods {
		fmt.Printf("Pending pods found: %d\n", len(pendingPods))
		fmt.Printf("Failed pods found: %d\n", len(failedPods))
		fmt.Printf("Other problematic pods found: %d\n", len(otherProblematicPods))
		fmt.Printf("Total pods to delete: %d\n", len(podsToDelete))
	}
	fmt.Printf("\n")

	if len(nodesToDelete) == 0 && len(podsToDelete) == 0 {
		fmt.Printf("✅ No nodes or pods need cleanup!\n")
		return
	}

	// Show nodes to be deleted
	if len(nodesToDelete) > 0 {
		fmt.Printf("🗑️  Nodes to delete:\n")
		for i, nodeName := range nodesToDelete {
			reason := deleteReasons[i]
			fmt.Printf("  - %s (%s)\n", nodeName, reason)
		}
		fmt.Printf("\n")
	}

	// Show pods to be deleted
	if len(podsToDelete) > 0 {
		fmt.Printf("🗑️  Pods to delete:\n")
		for i, podName := range podsToDelete {
			reason := podDeleteReasons[i]
			fmt.Printf("  - %s (%s)\n", podName, reason)
		}
		fmt.Printf("\n")
	}

	if *dryRun {
		fmt.Printf("✅ DRY RUN complete. Use --force flag to actually delete nodes")
		if *includePods {
			fmt.Printf(" and pods")
		}
		fmt.Printf(".\n")
		return
	}

	// Confirm deletion
	if !*force {
		totalItems := len(nodesToDelete)
		if *includePods {
			totalItems += len(podsToDelete)
		}

		fmt.Printf("⚠️  This will permanently delete %d items from the cluster:\n", totalItems)
		if len(nodesToDelete) > 0 {
			fmt.Printf("  - %d hollow nodes\n", len(nodesToDelete))
		}
		if len(podsToDelete) > 0 {
			fmt.Printf("  - %d pods\n", len(podsToDelete))
		}
		fmt.Printf("Press Ctrl+C to cancel or press Enter to continue...")
		fmt.Scanln()
	}

	// Delete nodes
	stats := &CleanupStats{}
	deleteOptions := &metav1.DeleteOptions{}

	if *force {
		// Force delete with zero grace period
		gracePeriod := int64(0)
		deleteOptions.GracePeriodSeconds = &gracePeriod
	}

	fmt.Printf("🧹 Starting cleanup of %d nodes...\n", len(nodesToDelete))

	// Create a worker pool for concurrent deletions
	nodeChan := make(chan struct{ name, reason string }, len(nodesToDelete))
	var wg sync.WaitGroup

	// Start concurrent workers
	maxWorkers := *concurrency
	if maxWorkers > len(nodesToDelete) {
		maxWorkers = len(nodesToDelete)
	}

	// Mutex for thread-safe stats updates and progress tracking
	var statsMutex sync.Mutex
	completed := 0

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for nodeInfo := range nodeChan {
				err := clientset.CoreV1().Nodes().Delete(ctx, nodeInfo.name, *deleteOptions)

				statsMutex.Lock()
				completed++
				if err != nil {
					stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", nodeInfo.name, err))
				} else {
					if nodeInfo.reason == "zombie" {
						stats.ZombieNodesDeleted++
					} else {
						stats.NotReadyNodesDeleted++
					}
				}

				// Show progress every 10% or at least every 100 nodes
				progressInterval := len(nodesToDelete) / 10
				if progressInterval == 0 || progressInterval > 100 {
					progressInterval = 100
				}

				if completed%progressInterval == 0 || completed == len(nodesToDelete) {
					percentage := (completed * 100) / len(nodesToDelete)
					fmt.Printf("Progress: %d/%d (%d%%) nodes processed\n", completed, len(nodesToDelete), percentage)
				}
				statsMutex.Unlock()

				// Optional small delay if configured
				if *delayMs > 0 {
					time.Sleep(time.Duration(*delayMs) * time.Millisecond)
				}
			}
		}()
	}

	// Send nodes to workers
	for i, nodeName := range nodesToDelete {
		reason := deleteReasons[i]
		nodeChan <- struct{ name, reason string }{nodeName, reason}
	}
	close(nodeChan)

	// Wait for all deletions to complete
	wg.Wait()

	// Delete pods if requested
	if *includePods && len(podsToDelete) > 0 {
		fmt.Printf("🧹 Starting cleanup of %d pods...\n", len(podsToDelete))

		// Create a worker pool for concurrent pod deletions
		podChan := make(chan struct{ name, reason string }, len(podsToDelete))
		var podWg sync.WaitGroup

		// Start concurrent workers for pods
		maxPodWorkers := *concurrency
		if maxPodWorkers > len(podsToDelete) {
			maxPodWorkers = len(podsToDelete)
		}

		podCompleted := 0

		for i := 0; i < maxPodWorkers; i++ {
			podWg.Add(1)
			go func() {
				defer podWg.Done()
				for podInfo := range podChan {
					err := clientset.CoreV1().Pods(*namespace).Delete(ctx, podInfo.name, *deleteOptions)

					statsMutex.Lock()
					podCompleted++
					if err != nil {
						stats.Errors = append(stats.Errors, fmt.Sprintf("pod %s: %v", podInfo.name, err))
					} else {
						switch podInfo.reason {
						case "pending":
							stats.PendingPodsDeleted++
						case "failed":
							stats.FailedPodsDeleted++
						default:
							stats.OtherPodsDeleted++
						}
					}

					// Show progress every 10% or at least every 50 pods
					podProgressInterval := len(podsToDelete) / 10
					if podProgressInterval == 0 || podProgressInterval > 50 {
						podProgressInterval = 50
					}

					if podCompleted%podProgressInterval == 0 || podCompleted == len(podsToDelete) {
						percentage := (podCompleted * 100) / len(podsToDelete)
						fmt.Printf("Pod Progress: %d/%d (%d%%) pods processed\n", podCompleted, len(podsToDelete), percentage)
					}
					statsMutex.Unlock()

					// Optional small delay if configured
					if *delayMs > 0 {
						time.Sleep(time.Duration(*delayMs) * time.Millisecond)
					}
				}
			}()
		}

		// Send pods to workers
		for i, podName := range podsToDelete {
			reason := podDeleteReasons[i]
			podChan <- struct{ name, reason string }{podName, reason}
		}
		close(podChan)

		// Wait for all pod deletions to complete
		podWg.Wait()
	}

	// Final summary
	fmt.Printf("\n🎯 Cleanup Complete!\n")
	fmt.Printf("Zombie nodes deleted: %d\n", stats.ZombieNodesDeleted)
	fmt.Printf("NotReady nodes deleted: %d\n", stats.NotReadyNodesDeleted)
	fmt.Printf("Total nodes deleted: %d\n", stats.ZombieNodesDeleted+stats.NotReadyNodesDeleted)

	if *includePods {
		fmt.Printf("Pending pods deleted: %d\n", stats.PendingPodsDeleted)
		fmt.Printf("Failed pods deleted: %d\n", stats.FailedPodsDeleted)
		fmt.Printf("Other pods deleted: %d\n", stats.OtherPodsDeleted)
		fmt.Printf("Total pods deleted: %d\n", stats.PendingPodsDeleted+stats.FailedPodsDeleted+stats.OtherPodsDeleted)
	}

	if len(stats.Errors) > 0 {
		fmt.Printf("\n❌ Errors encountered:\n")
		for _, errMsg := range stats.Errors {
			fmt.Printf("  - %s\n", errMsg)
		}
	} else {
		successMsg := "✅ All nodes deleted successfully!"
		if *includePods {
			successMsg = "✅ All nodes and pods deleted successfully!"
		}
		fmt.Printf("%s\n", successMsg)
	}
}

// deleteByLabelSelector lists all nodes matching the selector (unpaginated) and
// deletes them concurrently via a goroutine worker pool.
func deleteByLabelSelector(ctx context.Context, clientset *kubernetes.Clientset, selector string, concurrency, delayMs int, force, dryRun bool) {
	fmt.Printf("📋 Listing nodes matching %q (unpaginated) ...\n", selector)

	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing nodes: %v\n", err)
		os.Exit(1)
	}

	// Extract just the names and release the full node objects
	nodeNames := make([]string, len(nodeList.Items))
	for i := range nodeList.Items {
		nodeNames[i] = nodeList.Items[i].Name
	}
	nodeList = nil // allow GC to reclaim

	totalNodes := int64(len(nodeNames))
	fmt.Printf("📊 Nodes matching %q: %d\n\n", selector, totalNodes)

	if totalNodes == 0 {
		fmt.Printf("✅ No nodes match selector — nothing to do.\n")
		return
	}

	if dryRun {
		fmt.Printf("✅ DRY RUN complete. %d nodes would be deleted.\n", totalNodes)
		return
	}

	if !force {
		fmt.Printf("⚠️  This will permanently delete %d nodes.\n", totalNodes)
		fmt.Printf("Press Ctrl+C to cancel or press Enter to continue...")
		fmt.Scanln()
	}

	// Prepare delete options
	deleteOptions := metav1.DeleteOptions{}
	if force {
		gracePeriod := int64(0)
		deleteOptions.GracePeriodSeconds = &gracePeriod
	}

	// Worker pool
	nameCh := make(chan string, concurrency*2)
	var wg sync.WaitGroup

	var deleted int64
	var errCount int64
	start := time.Now()

	workers := concurrency
	if int64(workers) > totalNodes {
		workers = int(totalNodes)
	}

	const maxRetries = 3

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range nameCh {
				var lastErr error
				ok := false
				for attempt := 0; attempt < maxRetries; attempt++ {
					lastErr = clientset.CoreV1().Nodes().Delete(ctx, name, deleteOptions)
					if lastErr == nil {
						ok = true
						break
					}
					// "not found" means already deleted — treat as success
					if apierrors.IsNotFound(lastErr) {
						ok = true
						break
					}
					// Retry on transient server errors (GOAWAY, 429, 5xx)
					if apierrors.IsServerTimeout(lastErr) || apierrors.IsTooManyRequests(lastErr) ||
						apierrors.IsInternalError(lastErr) || apierrors.IsServiceUnavailable(lastErr) {
						time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
						continue
					}
					// Non-retryable error
					break
				}

				if ok {
					cur := atomic.AddInt64(&deleted, 1)
					if cur%1000 == 0 || cur == totalNodes {
						elapsed := time.Since(start).Seconds()
						rate := float64(cur) / elapsed
						fmt.Printf("Progress: %d/%d (%.0f nodes/sec)\n", cur, totalNodes, rate)
					}
				} else {
					cnt := atomic.AddInt64(&errCount, 1)
					if cnt <= 5 {
						fmt.Fprintf(os.Stderr, "❌ %s: %v\n", name, lastErr)
					}
				}

				if delayMs > 0 {
					time.Sleep(time.Duration(delayMs) * time.Millisecond)
				}
			}
		}()
	}

	// Feed names to workers
	for _, name := range nodeNames {
		nameCh <- name
	}
	close(nameCh)
	wg.Wait()

	elapsed := time.Since(start)
	finalDeleted := atomic.LoadInt64(&deleted)
	finalErrors := atomic.LoadInt64(&errCount)

	fmt.Printf("\n🎯 Label-selector cleanup complete!\n")
	fmt.Printf("Deleted: %d  Errors: %d  Elapsed: %s  (%.0f nodes/sec)\n",
		finalDeleted, finalErrors, elapsed.Round(time.Second), float64(finalDeleted)/elapsed.Seconds())
}
