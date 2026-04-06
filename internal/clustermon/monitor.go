package clustermon

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Snapshot captures a point-in-time view of cluster resources for a given run.
type Snapshot struct {
	Timestamp    time.Time          `json:"timestamp"`
	ElapsedSec   float64            `json:"elapsedSec"`
	TotalPods    int                `json:"totalPods"`
	RunningPods  int                `json:"runningPods"`
	PendingPods  int                `json:"pendingPods"`
	FailedPods   int                `json:"failedPods"`
	TotalNodes   int                `json:"totalNodes"`
	ReadyNodes   int                `json:"readyNodes"`
	Configmaps   int                `json:"configmaps"`
	Secrets      int                `json:"secrets"`
	Services     int                `json:"services"`
	Jobs         int                `json:"jobs"`
	Namespaces   int                `json:"namespaces"`
	ResourceCounts map[string]int   `json:"resourceCounts"`
	APIHealthMs  float64            `json:"apiHealthMs"`
}

// Monitor polls the Kubernetes cluster for resource counts.
type Monitor struct {
	client kubernetes.Interface
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

	return &Monitor{client: client}, nil
}

// TakeSnapshot queries the cluster and returns current resource counts.
// If runID is non-empty, pod/configmap/secret counts are filtered by the run-id label.
func (m *Monitor) TakeSnapshot(ctx context.Context, runID string, startTime time.Time) (*Snapshot, error) {
	snap := &Snapshot{
		Timestamp:      time.Now().UTC(),
		ElapsedSec:     time.Since(startTime).Seconds(),
		ResourceCounts: make(map[string]int),
	}

	// API health check
	healthStart := time.Now()
	_, err := m.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	snap.APIHealthMs = float64(time.Since(healthStart).Milliseconds())
	if err != nil {
		return snap, fmt.Errorf("API health check failed: %w", err)
	}

	labelSelector := ""
	if runID != "" {
		labelSelector = "run-id=" + runID
	}

	// Nodes (always unfiltered — we want the full cluster picture)
	nodes, err := m.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		snap.TotalNodes = len(nodes.Items)
		for _, n := range nodes.Items {
			for _, c := range n.Status.Conditions {
				if c.Type == "Ready" && c.Status == "True" {
					snap.ReadyNodes++
				}
			}
		}
		snap.ResourceCounts["nodes"] = snap.TotalNodes
	}

	// Pods (filtered by run-id if set)
	pods, err := m.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		snap.TotalPods = len(pods.Items)
		for _, p := range pods.Items {
			switch p.Status.Phase {
			case "Running":
				snap.RunningPods++
			case "Pending":
				snap.PendingPods++
			case "Failed":
				snap.FailedPods++
			}
		}
		snap.ResourceCounts["pods"] = snap.TotalPods
	}

	// ConfigMaps
	cms, err := m.client.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		snap.Configmaps = len(cms.Items)
		snap.ResourceCounts["configmaps"] = snap.Configmaps
	}

	// Secrets
	secrets, err := m.client.CoreV1().Secrets("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		snap.Secrets = len(secrets.Items)
		snap.ResourceCounts["secrets"] = snap.Secrets
	}

	// Services
	svcs, err := m.client.CoreV1().Services("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		snap.Services = len(svcs.Items)
		snap.ResourceCounts["services"] = snap.Services
	}

	// Jobs (batch/v1)
	jobs, err := m.client.BatchV1().Jobs("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		snap.Jobs = len(jobs.Items)
		snap.ResourceCounts["jobs"] = snap.Jobs
	}

	// Namespaces (filtered by run-id if available)
	ns, err := m.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		snap.Namespaces = len(ns.Items)
		snap.ResourceCounts["namespaces"] = snap.Namespaces
	}

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
