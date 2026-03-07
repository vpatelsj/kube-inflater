package nodes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labelsPkg "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ListKubemarkNodesForRun returns names and ready count for hollow nodes belonging to a specific run-id (daemonset name).
// Uses unpaginated list to avoid stale resource-version issues during rapid node creation.
func ListKubemarkNodesForRun(ctx context.Context, client *kubernetes.Clientset, runID string) ([]string, int, error) {
	selector := labelsPkg.Set{"kubemark": "true", "run-id": runID}.AsSelector().String()
	lst, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(lst.Items))
	ready := 0
	for _, n := range lst.Items {
		names = append(names, n.Name)
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}
	return names, ready, nil
}
