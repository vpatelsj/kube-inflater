package clustermon

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Snapshot captures a point-in-time view of cluster resources for a given run.
type Snapshot struct {
	Timestamp       time.Time      `json:"timestamp"`
	ElapsedSec      float64        `json:"elapsedSec"`
	TotalPods       int            `json:"totalPods"`
	RunningPods     int            `json:"runningPods"`
	PendingPods     int            `json:"pendingPods"`
	FailedPods      int            `json:"failedPods"`
	TotalNodes      int            `json:"totalNodes"`
	ReadyNodes      int            `json:"readyNodes"`
	Configmaps      int            `json:"configmaps"`
	Secrets         int            `json:"secrets"`
	Services        int            `json:"services"`
	Jobs            int            `json:"jobs"`
	Namespaces      int            `json:"namespaces"`
	ServiceAccounts int            `json:"serviceAccounts"`
	StatefulSets    int            `json:"statefulsets"`
	CustomResources int            `json:"customResources"`
	ResourceCounts  map[string]int `json:"resourceCounts"`
	APIHealthMs     float64        `json:"apiHealthMs"`

	// Cluster-wide totals (unfiltered by run-id)
	ClusterPods            int `json:"clusterPods"`
	ClusterConfigmaps      int `json:"clusterConfigmaps"`
	ClusterSecrets         int `json:"clusterSecrets"`
	ClusterServices        int `json:"clusterServices"`
	ClusterJobs            int `json:"clusterJobs"`
	ClusterNamespaces      int `json:"clusterNamespaces"`
	ClusterServiceAccounts int `json:"clusterServiceAccounts"`
	ClusterStatefulSets    int `json:"clusterStatefulsets"`
	ClusterCustomResources int `json:"clusterCustomResources"`

	// Watch connections (from apiserver metrics)
	WatchConnections int `json:"watchConnections"`
}

// Monitor polls the Kubernetes cluster for resource counts.
type Monitor struct {
	client     kubernetes.Interface
	dynClient  dynamic.Interface
	restConfig *rest.Config
}

var stressItemGVR = schema.GroupVersionResource{
	Group:    "stresstest.kube-inflater.io",
	Version:  "v1alpha1",
	Resource: "stressitems",
}

// New creates a Monitor, loading kubeconfig automatically.
func New() (*Monitor, error) {
	config, err := loadKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	config.QPS = 50
	config.Burst = 100

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &Monitor{client: client, dynClient: dynClient, restConfig: config}, nil
}

// TakeSnapshot queries the cluster and returns current resource counts.
// If runID is non-empty, pod/configmap/secret counts are filtered by the run-id label.
// All LIST calls run in parallel with ResourceVersion="0" (served from watch cache).
func (m *Monitor) TakeSnapshot(ctx context.Context, runID string, startTime time.Time) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Independent context so browser disconnects don't cancel in-flight K8s calls.
	apiCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap := &Snapshot{
		Timestamp:      time.Now().UTC(),
		ElapsedSec:     time.Since(startTime).Seconds(),
		ResourceCounts: make(map[string]int),
	}

	// API health check (not cached — measures real latency)
	healthStart := time.Now()
	_, err := m.client.CoreV1().Namespaces().List(apiCtx, metav1.ListOptions{Limit: 1})
	snap.APIHealthMs = float64(time.Since(healthStart).Milliseconds())
	if err != nil {
		return snap, fmt.Errorf("API health check failed: %w", err)
	}

	labelSelector := ""
	if runID != "" {
		labelSelector = "run-id=" + runID
	}

	// All LIST calls use ResourceVersion="0" to serve from watch cache.
	// To avoid OOM at scale, most queries use Limit:1 + RemainingItemCount for counting.
	cached := func(opts metav1.ListOptions) metav1.ListOptions {
		opts.ResourceVersion = "0"
		return opts
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	// Helper to run a LIST call in a goroutine and invoke fn with the result.
	do := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	// countFromList derives the total count from a Limit:1 list response.
	countFromList := func(fetched int, remaining *int64) int {
		if remaining != nil {
			return fetched + int(*remaining)
		}
		return fetched
	}

	// --- Run-scoped queries (use Limit:1 counting where possible) ---

	do(func() {
		// Nodes need Ready condition — paginate in small batches to avoid pulling all at once.
		var total, ready int
		cont := ""
		for {
			opts := cached(metav1.ListOptions{Limit: 500})
			opts.Continue = cont
			nodes, err := m.client.CoreV1().Nodes().List(apiCtx, opts)
			if err != nil {
				return
			}
			total += len(nodes.Items)
			for _, n := range nodes.Items {
				for _, c := range n.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						ready++
					}
				}
			}
			cont = nodes.Continue
			if cont == "" {
				break
			}
		}
		mu.Lock()
		snap.TotalNodes = total
		snap.ReadyNodes = ready
		snap.ResourceCounts["nodes"] = total
		mu.Unlock()
	})

	do(func() {
		// Pods need phase breakdown — paginate in small batches.
		var total, running, pending, failed int
		cont := ""
		for {
			opts := cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 500})
			opts.Continue = cont
			pods, err := m.client.CoreV1().Pods("").List(apiCtx, opts)
			if err != nil {
				return
			}
			total += len(pods.Items)
			for _, p := range pods.Items {
				switch p.Status.Phase {
				case "Running":
					running++
				case "Pending":
					pending++
				case "Failed":
					failed++
				}
			}
			cont = pods.Continue
			if cont == "" {
				break
			}
		}
		mu.Lock()
		snap.TotalPods = total
		snap.RunningPods = running
		snap.PendingPods = pending
		snap.FailedPods = failed
		snap.ResourceCounts["pods"] = total
		mu.Unlock()
	})

	do(func() {
		cms, err := m.client.CoreV1().ConfigMaps("").List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.Configmaps = countFromList(len(cms.Items), cms.RemainingItemCount)
			snap.ResourceCounts["configmaps"] = snap.Configmaps
			mu.Unlock()
		}
	})

	do(func() {
		secrets, err := m.client.CoreV1().Secrets("").List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.Secrets = countFromList(len(secrets.Items), secrets.RemainingItemCount)
			snap.ResourceCounts["secrets"] = snap.Secrets
			mu.Unlock()
		}
	})

	do(func() {
		svcs, err := m.client.CoreV1().Services("").List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.Services = countFromList(len(svcs.Items), svcs.RemainingItemCount)
			snap.ResourceCounts["services"] = snap.Services
			mu.Unlock()
		}
	})

	do(func() {
		jobs, err := m.client.BatchV1().Jobs("").List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.Jobs = countFromList(len(jobs.Items), jobs.RemainingItemCount)
			snap.ResourceCounts["jobs"] = snap.Jobs
			mu.Unlock()
		}
	})

	do(func() {
		ns, err := m.client.CoreV1().Namespaces().List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.Namespaces = countFromList(len(ns.Items), ns.RemainingItemCount)
			snap.ResourceCounts["namespaces"] = snap.Namespaces
			mu.Unlock()
		}
	})

	do(func() {
		sas, err := m.client.CoreV1().ServiceAccounts("").List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ServiceAccounts = countFromList(len(sas.Items), sas.RemainingItemCount)
			snap.ResourceCounts["serviceaccounts"] = snap.ServiceAccounts
			mu.Unlock()
		}
	})

	do(func() {
		sts, err := m.client.AppsV1().StatefulSets("").List(apiCtx, cached(metav1.ListOptions{LabelSelector: labelSelector, Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.StatefulSets = countFromList(len(sts.Items), sts.RemainingItemCount)
			snap.ResourceCounts["statefulsets"] = snap.StatefulSets
			mu.Unlock()
		}
	})

	do(func() {
		crs, err := m.dynClient.Resource(stressItemGVR).Namespace("").List(apiCtx, metav1.ListOptions{LabelSelector: labelSelector, ResourceVersion: "0", Limit: 1})
		if err == nil {
			remaining := crs.GetRemainingItemCount()
			mu.Lock()
			snap.CustomResources = countFromList(len(crs.Items), remaining)
			snap.ResourceCounts["customresources"] = snap.CustomResources
			mu.Unlock()
		}
	})

	// --- Cluster-wide totals (Limit:1 + RemainingItemCount) ---

	do(func() {
		all, err := m.client.CoreV1().Pods("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterPods = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.CoreV1().ConfigMaps("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterConfigmaps = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.CoreV1().Secrets("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterSecrets = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.CoreV1().Services("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterServices = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.BatchV1().Jobs("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterJobs = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.CoreV1().Namespaces().List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterNamespaces = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.CoreV1().ServiceAccounts("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterServiceAccounts = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.client.AppsV1().StatefulSets("").List(apiCtx, cached(metav1.ListOptions{Limit: 1}))
		if err == nil {
			mu.Lock()
			snap.ClusterStatefulSets = countFromList(len(all.Items), all.RemainingItemCount)
			mu.Unlock()
		}
	})

	do(func() {
		all, err := m.dynClient.Resource(stressItemGVR).Namespace("").List(apiCtx, metav1.ListOptions{ResourceVersion: "0", Limit: 1})
		if err == nil {
			remaining := all.GetRemainingItemCount()
			mu.Lock()
			snap.ClusterCustomResources = countFromList(len(all.Items), remaining)
			mu.Unlock()
		}
	})

	// Watch connections from apiserver metrics
	do(func() {
		count := m.scrapeWatchCount(apiCtx)
		mu.Lock()
		snap.WatchConnections = count
		mu.Unlock()
	})

	wg.Wait()
	return snap, nil
}

func loadKubeConfig() (*rest.Config, error) {
	// Try in-cluster first
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	// Fall back to kubeconfig
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}

// scrapeWatchCount fetches /metrics from the apiserver and sums all
// apiserver_longrunning_requests gauge values with verb="WATCH".
func (m *Monitor) scrapeWatchCount(ctx context.Context) int {
	client, err := kubernetes.NewForConfig(m.restConfig)
	if err != nil {
		return 0
	}
	body, err := client.RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
	if err != nil {
		return 0
	}

	total := 0
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "apiserver_longrunning_requests{") {
			continue
		}
		if !strings.Contains(line, `verb="WATCH"`) {
			continue
		}
		// Format: metric_name{labels} value
		idx := strings.LastIndex(line, " ")
		if idx < 0 {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(line[idx+1:]), 64)
		if err == nil {
			total += int(val)
		}
	}
	return total
}
