package nodes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labelsPkg "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ListKubemarkNodes returns names and the count of Ready nodes with label kubemark=true.
// Uses unpaginated list to avoid stale resource-version issues during rapid node creation.
func ListKubemarkNodes(ctx context.Context, client *kubernetes.Clientset) ([]string, int, error) {
	labelSel := labelsPkg.SelectorFromSet(labelsPkg.Set{"kubemark": "true"}).String()
	lst, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: labelSel})
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(lst.Items))
	totalReady := 0
	for _, n := range lst.Items {
		names = append(names, n.Name)
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				totalReady++
				break
			}
		}
	}
	return names, totalReady, nil
}
