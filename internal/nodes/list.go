package nodes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labelsPkg "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ListKubemarkNodes returns names and the count of Ready nodes with label kubemark=true
func ListKubemarkNodes(ctx context.Context, client *kubernetes.Clientset) ([]string, int, error) {
	var names []string
	labelSel := labelsPkg.SelectorFromSet(labelsPkg.Set{"kubemark": "true"}).String()
	cont := ""
	totalReady := 0
	for i := 0; i < 100; i++ {
		lst, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: labelSel, Limit: 2000, Continue: cont})
		if err != nil {
			return names, 0, err
		}
		for _, n := range lst.Items {
			names = append(names, n.Name)
			for _, cond := range n.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					totalReady++
					break
				}
			}
		}
		if lst.Continue == "" {
			break
		}
		cont = lst.Continue
	}
	return names, totalReady, nil
}
