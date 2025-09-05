package nodes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labelsPkg "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ListKubemarkNodesForRun returns names and ready count for hollow nodes belonging to a specific run-id (daemonset name)
func ListKubemarkNodesForRun(ctx context.Context, client *kubernetes.Clientset, runID string) ([]string, int, error) {
	selector := labelsPkg.Set{"kubemark": "true", "run-id": runID}.AsSelector().String()
	cont := ""
	var names []string
	ready := 0
	for i := 0; i < 100; i++ {
		lst, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 2000, Continue: cont})
		if err != nil {
			return names, 0, err
		}
		for _, n := range lst.Items {
			names = append(names, n.Name)
			for _, cond := range n.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					ready++
					break
				}
			}
		}
		if lst.Continue == "" {
			break
		}
		cont = lst.Continue
	}
	return names, ready, nil
}
