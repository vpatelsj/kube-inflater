package resourcegen

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/daemonsetspec"
	"kube-inflater/internal/nodes"
)

// DefaultTokenAudiences lists common API server audiences for SA tokens.
var DefaultTokenAudiences = []string{
	"https://kubernetes.default.svc.cluster.local",
	"https://kubernetes.default.svc",
	"kubernetes", "k3s", "kube-apiserver", "api",
}

// HollowNodeGenerator implements SetupTeardownGenerator for hollow kubemark nodes.
// Instead of creating resources one-by-one, it deploys a DaemonSet whose pods
// register hollow nodes via kubemark containers.
type HollowNodeGenerator struct {
	opts          cfgpkg.HollowNodeOpts
	daemonSetName string
}

// NewHollowNodeGenerator creates a HollowNodeGenerator from opts.
// If opts is nil, sensible defaults are used.
func NewHollowNodeGenerator(opts *cfgpkg.HollowNodeOpts) *HollowNodeGenerator {
	var o cfgpkg.HollowNodeOpts
	if opts != nil {
		o = *opts
	}
	if o.ContainersPerPod <= 0 {
		o.ContainersPerPod = cfgpkg.DefaultContainersPerPod
	}
	if o.KubemarkImage == "" {
		o.KubemarkImage = cfgpkg.DefaultKubemarkImage
	}
	if len(o.TokenAudiences) == 0 {
		o.TokenAudiences = DefaultTokenAudiences
	}
	if o.NodeStatusFreq == "" {
		o.NodeStatusFreq = "60s"
	}
	if o.NodeLeaseDuration <= 0 {
		o.NodeLeaseDuration = 240
	}
	if o.NodeMonitorGrace == "" {
		o.NodeMonitorGrace = "240s"
	}
	if o.Namespace == "" {
		o.Namespace = cfgpkg.DefaultNamespace
	}
	if o.WaitTimeout <= 0 {
		o.WaitTimeout = time.Duration(cfgpkg.DefaultWaitTimeoutSec) * time.Second
	}
	if o.RetainDaemonSets <= 0 {
		o.RetainDaemonSets = 1
	}
	return &HollowNodeGenerator{opts: o}
}

// --- ResourceGenerator interface (minimal) ---

func (h *HollowNodeGenerator) Generate(_ string, _ string, _ int) (*unstructured.Unstructured, error) {
	// No-op: nodes are created implicitly by the DaemonSet.
	return nil, nil
}

func (h *HollowNodeGenerator) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
}

func (h *HollowNodeGenerator) IsNamespaced() bool { return false }
func (h *HollowNodeGenerator) Kind() string       { return "Node" }
func (h *HollowNodeGenerator) TypeName() string   { return "hollownodes" }

// --- SetupTeardownGenerator interface ---

func (h *HollowNodeGenerator) IsSetupBased() bool { return true }

// maxContainersPerPod is the practical upper limit of kubemark containers
// a single DaemonSet pod can run on one physical node.
const maxContainersPerPod = 200

// Setup creates the hollow node DaemonSet and all prerequisites.
// count is the desired total hollow node count. containersPerPod is
// automatically scaled up when the cluster does not have enough
// schedulable nodes to reach the requested count, capped at 200 per node.
func (h *HollowNodeGenerator) Setup(ctx context.Context, client kubernetes.Interface, _ dynamic.Interface, runID string, count int, dryRun bool) (int, error) {
	h.daemonSetName = h.generateDaemonSetName(runID)

	if dryRun {
		logHollow("[DRY-RUN] Would create hollow node DaemonSet %s in %s (containers-per-pod=%d)",
			h.daemonSetName, h.opts.Namespace, h.opts.ContainersPerPod)
		return count, nil
	}

	// Auto-scale containersPerPod so that schedulableNodes * containersPerPod >= count
	schedulable, err := h.countSchedulableNodes(ctx, client)
	if err != nil {
		logHollow("Warning: could not count schedulable nodes: %v (using containers-per-pod=%d)", err, h.opts.ContainersPerPod)
	} else if schedulable > 0 {
		needed := (count + schedulable - 1) / schedulable // ceil division
		if needed > maxContainersPerPod {
			maxCount := schedulable * maxContainersPerPod
			logHollow("Requested %d hollow nodes exceeds max capacity (%d nodes × %d max containers = %d). Capping to %d.",
				count, schedulable, maxContainersPerPod, maxCount, maxCount)
			needed = maxContainersPerPod
			count = maxCount
		}
		if needed > h.opts.ContainersPerPod {
			logHollow("Auto-scaling containers-per-pod from %d to %d (%d schedulable nodes, %d hollow nodes requested)",
				h.opts.ContainersPerPod, needed, schedulable, count)
			h.opts.ContainersPerPod = needed
		}
	} else {
		logHollow("Warning: no schedulable worker nodes found for hollow node DaemonSet")
	}

	if err := h.ensureNamespace(ctx, client); err != nil {
		return 0, fmt.Errorf("ensuring namespace: %w", err)
	}
	if err := h.ensureServiceAccount(ctx, client); err != nil {
		return 0, fmt.Errorf("ensuring service account: %w", err)
	}
	if err := h.createKubeconfigSecret(ctx, client); err != nil {
		return 0, fmt.Errorf("creating kubeconfig secret: %w", err)
	}
	if h.opts.PrunePrevious {
		h.pruneOldDaemonSets(ctx, client)
	}
	if err := h.createDaemonSet(ctx, client); err != nil {
		return 0, fmt.Errorf("creating DaemonSet: %w", err)
	}

	// Return the actual expected count (may exceed requested count due to ceil rounding)
	expected := count
	if schedulable > 0 {
		expected = schedulable * h.opts.ContainersPerPod
	}
	return expected, nil
}

// WaitForReady polls until the expected number of hollow nodes are registered and Ready.
func (h *HollowNodeGenerator) WaitForReady(ctx context.Context, client kubernetes.Interface, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = h.opts.WaitTimeout
	}
	deadline := time.Now().Add(timeout)
	logHollow("Waiting up to %v for hollow nodes to become ready (DaemonSet %s)...", timeout, h.daemonSetName)

	var lastExpected int
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}

		expected := h.deriveExpectedCount(ctx, client)
		if expected != lastExpected && expected > 0 {
			logHollow("Expected hollow nodes (pods × containersPerPod): %d", expected)
			lastExpected = expected
		}

		_, readyCount, err := nodes.ListKubemarkNodesForRun(ctx, client, h.daemonSetName)
		if err != nil {
			logHollow("Warning: failed listing nodes: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if expected > 0 {
			logHollow("Ready hollow nodes: %d / %d", readyCount, expected)
		}
		if expected > 0 && readyCount >= expected {
			logHollow("All expected hollow nodes registered and ready")
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for hollow nodes (DaemonSet %s)", h.daemonSetName)
}

// Teardown removes the DaemonSet, hollow nodes, and all supporting resources.
func (h *HollowNodeGenerator) Teardown(ctx context.Context, client kubernetes.Interface, _ dynamic.Interface, dryRun bool) error {
	if dryRun {
		logHollow("[DRY-RUN] Would clean up hollow node resources in %s", h.opts.Namespace)
		return nil
	}

	logHollow("Starting hollow node cleanup...")

	// Delete all hollow-node DaemonSets
	dsList, err := client.AppsV1().DaemonSets(h.opts.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=hollow-node",
	})
	if err == nil {
		for _, ds := range dsList.Items {
			logHollow("Deleting DaemonSet %s", ds.Name)
			_ = client.AppsV1().DaemonSets(h.opts.Namespace).Delete(ctx, ds.Name, metav1.DeleteOptions{})
		}
	}

	// Delete hollow nodes
	nodeNames, _, err := nodes.ListKubemarkNodes(ctx, client)
	if err == nil && len(nodeNames) > 0 {
		logHollow("Deleting %d hollow nodes...", len(nodeNames))
		deleted := 0
		for _, name := range nodeNames {
			_ = client.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
			deleted++
			if deleted%100 == 0 || deleted == len(nodeNames) {
				logHollow("  %d/%d hollow nodes deleted", deleted, len(nodeNames))
			}
		}
	}

	// Delete supporting resources
	logHollow("Deleting kubeconfig secret")
	_ = client.CoreV1().Secrets(h.opts.Namespace).Delete(ctx, "hollow-node-kubeconfig", metav1.DeleteOptions{})

	logHollow("Deleting service account")
	_ = client.CoreV1().ServiceAccounts(h.opts.Namespace).Delete(ctx, "hollow-node", metav1.DeleteOptions{})

	logHollow("Deleting cluster role binding")
	_ = client.RbacV1().ClusterRoleBindings().Delete(ctx, "hollow-node-binding", metav1.DeleteOptions{})

	if h.opts.Namespace == cfgpkg.DefaultNamespace {
		logHollow("Deleting namespace %s", h.opts.Namespace)
		_ = client.CoreV1().Namespaces().Delete(ctx, h.opts.Namespace, metav1.DeleteOptions{})
	}

	logHollow("Hollow node cleanup completed")
	return nil
}

// --- internal helpers ---

func (h *HollowNodeGenerator) generateDaemonSetName(runID string) string {
	ts := time.Now().UTC().Format("20060102-150405")
	suffix := rand.Intn(10000)
	return fmt.Sprintf("hollow-nodes-%s-%04d", ts, suffix)
}

func (h *HollowNodeGenerator) ensureNamespace(ctx context.Context, client kubernetes.Interface) error {
	_, err := client.CoreV1().Namespaces().Get(ctx, h.opts.Namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	logHollow("Creating namespace %s", h.opts.Namespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: h.opts.Namespace}}
	_, err = client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	return err
}

func (h *HollowNodeGenerator) ensureServiceAccount(ctx context.Context, client kubernetes.Interface) error {
	saName := "hollow-node"
	_, err := client.CoreV1().ServiceAccounts(h.opts.Namespace).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	logHollow("Creating ServiceAccount %s", saName)
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: h.opts.Namespace},
	}
	if _, err := client.CoreV1().ServiceAccounts(h.opts.Namespace).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		return err
	}

	crbName := "hollow-node-binding"
	_, err = client.RbacV1().ClusterRoleBindings().Get(ctx, crbName, metav1.GetOptions{})
	if err != nil {
		logHollow("Creating ClusterRoleBinding %s", crbName)
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: crbName},
			Subjects: []rbacv1.Subject{{
				Kind: "ServiceAccount", Name: saName, Namespace: h.opts.Namespace,
			}},
			RoleRef: rbacv1.RoleRef{
				Kind: "ClusterRole", Name: "system:node", APIGroup: "rbac.authorization.k8s.io",
			},
		}
		if _, err := client.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (h *HollowNodeGenerator) createKubeconfigSecret(ctx context.Context, client kubernetes.Interface) error {
	secretName := "hollow-node-kubeconfig"
	_, err := client.CoreV1().Secrets(h.opts.Namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	logHollow("Creating ServiceAccount token for kubeconfig")
	tokenRequest := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences:         h.opts.TokenAudiences,
			ExpirationSeconds: func() *int64 { exp := int64(86400 * 30); return &exp }(),
		},
	}
	tokenResponse, err := client.CoreV1().ServiceAccounts(h.opts.Namespace).CreateToken(ctx, "hollow-node", tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating SA token: %w", err)
	}

	// Get cluster CA and server address
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}

	// Resolve CA data: prefer inline CAData, fall back to CAFile or in-cluster SA cert
	caData := restConfig.CAData
	if len(caData) == 0 && restConfig.CAFile != "" {
		caData, err = os.ReadFile(restConfig.CAFile)
		if err != nil {
			return fmt.Errorf("reading CA file %s: %w", restConfig.CAFile, err)
		}
	}
	if len(caData) == 0 {
		// In-cluster: read the service account CA bundle
		caData, err = os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
		if err != nil {
			return fmt.Errorf("reading in-cluster CA: %w", err)
		}
	}

	kubernetesService, err := client.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting kubernetes service: %w", err)
	}
	serverHost := fmt.Sprintf("https://%s:443", kubernetesService.Spec.ClusterIP)

	kubeconfigContent := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: %s
    server: %s
  name: cluster
contexts:
- context:
    cluster: cluster
    user: hollow-node
  name: hollow-node
current-context: hollow-node
users:
- name: hollow-node
  user:
    token: %s
`,
		base64.StdEncoding.EncodeToString(caData),
		serverHost,
		tokenResponse.Status.Token)

	logHollow("Creating kubeconfig secret %s", secretName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: h.opts.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"kubeconfig": []byte(kubeconfigContent)},
	}
	_, err = client.CoreV1().Secrets(h.opts.Namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func (h *HollowNodeGenerator) pruneOldDaemonSets(ctx context.Context, client kubernetes.Interface) {
	list, err := client.AppsV1().DaemonSets(h.opts.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=hollow-node",
	})
	if err != nil {
		return
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.Time.After(list.Items[j].CreationTimestamp.Time)
	})
	keep := h.opts.RetainDaemonSets - 1
	if keep < 0 {
		keep = 0
	}
	kept := 0
	for _, ds := range list.Items {
		if ds.Name == h.daemonSetName {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		logHollow("Pruning old DaemonSet %s", ds.Name)
		_ = client.AppsV1().DaemonSets(h.opts.Namespace).Delete(ctx, ds.Name, metav1.DeleteOptions{})
	}
}

func (h *HollowNodeGenerator) createDaemonSet(ctx context.Context, client kubernetes.Interface) error {
	cfg := h.toCfg()
	ds := daemonsetspec.MakeHollowDaemonSetSpec(cfg, h.daemonSetName, h.opts.ContainersPerPod)
	logHollow("Creating DaemonSet %s (containersPerPod=%d)", ds.Name, h.opts.ContainersPerPod)
	_, err := client.AppsV1().DaemonSets(h.opts.Namespace).Create(ctx, ds, metav1.CreateOptions{})
	return err
}

// countSchedulableNodes returns the number of nodes that match the DaemonSet's
// scheduling requirements (worker nodes, not control-plane, not kubemark, not KWOK, instance-type=k3s).
func (h *HollowNodeGenerator) countSchedulableNodes(ctx context.Context, client kubernetes.Interface) (int, error) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/worker,!node-role.kubernetes.io/control-plane,!kubemark,!type,node.kubernetes.io/instance-type=k3s",
	})
	if err != nil {
		return 0, err
	}
	return len(nodes.Items), nil
}

func (h *HollowNodeGenerator) deriveExpectedCount(ctx context.Context, client kubernetes.Interface) int {
	ds, err := client.AppsV1().DaemonSets(h.opts.Namespace).Get(ctx, h.daemonSetName, metav1.GetOptions{})
	if err != nil {
		return 0
	}
	pods := int(ds.Status.DesiredNumberScheduled)
	if pods == 0 {
		pods = int(ds.Status.CurrentNumberScheduled)
	}
	return pods * h.opts.ContainersPerPod
}

// toCfg builds the legacy config.Config needed by daemonsetspec.
func (h *HollowNodeGenerator) toCfg() *cfgpkg.Config {
	return &cfgpkg.Config{
		KubemarkImage:     h.opts.KubemarkImage,
		NodeStatusFreq:    h.opts.NodeStatusFreq,
		NodeLeaseDuration: h.opts.NodeLeaseDuration,
		NodeMonitorGrace:  h.opts.NodeMonitorGrace,
		Namespace:         h.opts.Namespace,
		ContainersPerPod:  h.opts.ContainersPerPod,
		DaemonSetName:     h.daemonSetName,
		TokenAudiences:    h.opts.TokenAudiences,
	}
}

// DaemonSetName returns the generated DaemonSet name (available after Setup).
func (h *HollowNodeGenerator) DaemonSetName() string { return h.daemonSetName }

func logHollow(format string, args ...interface{}) {
	fmt.Printf("[INFO] [hollownodes] "+format+"\n", args...)
}

// Ensure interface compliance at compile time.
var _ SetupTeardownGenerator = (*HollowNodeGenerator)(nil)
