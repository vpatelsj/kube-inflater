package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"math/rand"
	"sort"
	"strings"
	"syscall"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	cfgpkg "kube-inflater/internal/config"
	"kube-inflater/internal/daemonsetspec"
	"kube-inflater/internal/nodes"
)

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return def
}

func getenvStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfigFromFlags() *cfgpkg.Config {
	cfg := &cfgpkg.Config{
		WaitTimeout:       time.Duration(getenvInt("WAIT_TIMEOUT", cfgpkg.DefaultWaitTimeoutSec)) * time.Second,
		PerfWait:          time.Duration(getenvInt("PERFORMANCE_WAIT", cfgpkg.DefaultPerfWaitSec)) * time.Second,
		PerfTests:         getenvInt("PERFORMANCE_TESTS", cfgpkg.DefaultPerfTests),
		KubemarkImage:     getenvStr("KUBEMARK_IMAGE", cfgpkg.DefaultKubemarkImage),
		NodeStatusFreq:    getenvStr("NODE_STATUS_UPDATE_FREQUENCY", "60s"),
		NodeLeaseDuration: getenvInt("NODE_LEASE_DURATION", 120),
		NodeMonitorGrace:  getenvStr("NODE_MONITOR_GRACE_PERIOD", "240s"),
		ContainersPerPod:  getenvInt("CONTAINERS_PER_POD", cfgpkg.DefaultContainersPerPod),
		Namespace:         cfgpkg.DefaultNamespace,
		TokenAudiences:    cfgpkg.DefaultTokenAudiences,
	}

	timeoutSec := int(cfg.WaitTimeout / time.Second)
	perfWaitSec := int(cfg.PerfWait / time.Second)

	strictTarget := flag.Bool("strict-target", false, "(future) enforce expected hollow nodes strictly")
	autoExpected := flag.Bool("auto-expected", true, "Compute expected hollow nodes from daemonset pods * containers-per-pod for logging")
	flag.IntVar(&timeoutSec, "timeout", timeoutSec, "Seconds to wait for nodes to become ready")
	flag.IntVar(&perfWaitSec, "perf-wait", perfWaitSec, "Seconds to wait after deployment before measuring performance")
	flag.IntVar(&cfg.PerfTests, "perf-tests", cfg.PerfTests, "Number of API calls to test for performance measurement")
	flag.BoolVar(&cfg.SkipPerfTests, "skip-perf-tests", cfg.SkipPerfTests, "Skip performance tests entirely")
	flag.StringVar(&cfg.NodeStatusFreq, "node-status-frequency", cfg.NodeStatusFreq, "Kubelet node status update frequency")
	flag.IntVar(&cfg.NodeLeaseDuration, "node-lease-duration", cfg.NodeLeaseDuration, "Node lease duration (seconds)")
	flag.StringVar(&cfg.NodeMonitorGrace, "node-monitor-grace", cfg.NodeMonitorGrace, "Node monitor grace period")
	flag.IntVar(&cfg.ContainersPerPod, "containers-per-pod", cfg.ContainersPerPod, "Number of kubemark containers (nodes) per pod")
	flag.StringVar(&cfg.DaemonSetName, "daemonset-name", cfg.DaemonSetName, "Name for the hollow node daemonset (auto-generated if empty)")
	flag.BoolVar(&cfg.PrunePrevious, "prune-previous", false, "Prune older hollow-node daemonsets before creating a new one")
	flag.BoolVar(&cfg.CleanupOnly, "cleanup-only", false, "Only cleanup kubemark / hollow-node resources and exit")
	retain := 1
	flag.IntVar(&retain, "retain-daemonsets", retain, "Number of most recent hollow-node daemonsets to retain (includes the one being created). Use 0 to delete all previous.")
	tokenAudCSV := getenvStr("TOKEN_AUDIENCES", strings.Join(cfg.TokenAudiences, ","))
	tokenAudStr := tokenAudCSV
	flag.StringVar(&tokenAudStr, "token-audiences", tokenAudStr, "Comma-separated ServiceAccount token audiences for hollow-node kubeconfig")
	flag.Parse()

	cfg.WaitTimeout = time.Duration(timeoutSec) * time.Second
	cfg.PerfWait = time.Duration(perfWaitSec) * time.Second
	cfg.StrictTarget = *strictTarget
	cfg.AutoExpected = *autoExpected

	// Parse token audiences
	if tokenAudStr != "" {
		parts := strings.Split(tokenAudStr, ",")
		cfg.TokenAudiences = nil
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				cfg.TokenAudiences = append(cfg.TokenAudiences, p)
			}
		}
	}

	// Generate unique daemonset name if not provided
	if cfg.DaemonSetName == "" {
		// timestamp + random suffix to avoid collisions on fast reruns
		ts := time.Now().UTC().Format("20060102-150405")
		suffix := rand.Intn(10000)
		cfg.DaemonSetName = fmt.Sprintf("hollow-nodes-%s-%04d", ts, suffix)
	}
	cfg.RetainDaemonSets = retain

	// No node count flag anymore; CleanupOnly just triggers early exit later

	return cfg
}

type Clients struct {
	clientset      *kubernetes.Clientset
	discoveryCache discovery.DiscoveryInterface
}

func buildClients(cfg *cfgpkg.Config) (*Clients, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	restConfig, err := config.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}

	discoveryCache := clientset.Discovery()

	return &Clients{
		clientset:      clientset,
		discoveryCache: discoveryCache,
	}, nil
}

func logInfo(msg string) {
	fmt.Printf("[INFO] %s\n", msg)
}

func logWarn(msg string) {
	fmt.Printf("[WARN] %s\n", msg)
}

func logErr(msg string) {
	fmt.Printf("[ERROR] %s\n", msg)
}

func logPerf(msg string) {
	fmt.Printf("🎈 [GAUGE] %s\n", msg)
}

// Find the next deployment number by checking existing deployments
// createDaemonSet builds/creates the hollow node daemonset
func createDaemonSet(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) error {
	if cfg.PrunePrevious {
		list, err := clients.clientset.AppsV1().DaemonSets(cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=hollow-node"})
		if err == nil {
			// Sort by creation timestamp descending (newest first)
			sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].CreationTimestamp.Time.After(list.Items[j].CreationTimestamp.Time) })
			keep := cfg.RetainDaemonSets - 1 // minus the new one we will create
			if keep < 0 { keep = 0 }
			countKept := 0
			for _, ds := range list.Items {
				if ds.Name == cfg.DaemonSetName { // safety; unlikely since new name
					continue
				}
				if countKept < keep {
					countKept++
					continue
				}
				logInfo(fmt.Sprintf("Pruning old daemonset %s", ds.Name))
				_ = clients.clientset.AppsV1().DaemonSets(cfg.Namespace).Delete(ctx, ds.Name, metav1.DeleteOptions{})
			}
		} else {
			logWarn(fmt.Sprintf("Failed listing daemonsets for pruning: %v", err))
		}
	}
	daemonset := daemonsetspec.MakeHollowDaemonSetSpec(cfg, cfg.DaemonSetName, cfg.ContainersPerPod)
	logInfo(fmt.Sprintf("Creating daemonset %s (containersPerPod=%d)", daemonset.Name, cfg.ContainersPerPod))
	_, err := clients.clientset.AppsV1().DaemonSets(cfg.Namespace).Create(ctx, daemonset, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating daemonset: %w", err)
	}
	logInfo("Successfully created daemonset")
	return nil
}

func waitForNodes(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) error {
	deadline := time.Now().Add(cfg.WaitTimeout)
	logInfo("🎈 Inflating cluster (DaemonSet mode). Node count derives from daemonset pods * containers-per-pod.")
	var lastExpected int
	for time.Now().Before(deadline) {
		select { case <-ctx.Done(): return ctx.Err(); default: }
		// derive expected purely from daemonset
		expected := 0
		ds, err := clients.clientset.AppsV1().DaemonSets(cfg.Namespace).Get(ctx, cfg.DaemonSetName, metav1.GetOptions{})
		if err == nil {
			pods := int(ds.Status.DesiredNumberScheduled)
			// Fallback to CurrentNumberScheduled if Desired is still zero right after creation
			if pods == 0 {
				pods = int(ds.Status.CurrentNumberScheduled)
			}
			if pods > 0 { expected = pods * cfg.ContainersPerPod }
		}
		if expected != lastExpected {
			logInfo(fmt.Sprintf("[INFLATE] Expected hollow nodes (pods * containersPerPod): %d", expected))
			lastExpected = expected
		}
		_, readyCount, err := nodes.ListKubemarkNodesForRun(ctx, clients.clientset, cfg.DaemonSetName)
		if err != nil {
			logWarn(fmt.Sprintf("Failed to list nodes: %v", err))
			time.Sleep(5 * time.Second)
			continue
		}
		if expected > 0 {
			logInfo(fmt.Sprintf("[INFLATE] Ready hollow nodes: %d / %d", readyCount, expected))
		} else if readyCount > 0 {
			// If we already see hollow nodes but expected is still 0, just log transient state once
			if lastExpected == 0 { logInfo(fmt.Sprintf("[INFLATE] Hollow nodes registering (%d) before daemonset expectation calculated...", readyCount)) }
		}
		if expected > 0 && readyCount >= expected {
			logInfo("🎈 [FULL] All expected hollow nodes registered")
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for nodes to become ready (daemonset expected did not fully register)")
}

func runPerformanceTests(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) {
	if cfg.SkipPerfTests {
		logInfo("Skipping performance tests (--skip-perf-tests)")
		return
	}

	logPerf(fmt.Sprintf("Waiting %v before performance tests...", cfg.PerfWait))
	deadline := time.Now().Add(cfg.PerfWait)
	for {
		select {
		case <-ctx.Done():
			logInfo("Performance wait cancelled")
			return
		case <-time.After(1 * time.Second):
			if time.Now().After(deadline) { goto PERFSTART }
		}
	}

PERFSTART:

	logPerf(fmt.Sprintf("Running %d performance test calls", cfg.PerfTests))

	// Simple performance measurement - list nodes multiple times
	start := time.Now()
	for i := 0; i < cfg.PerfTests; i++ {
		_, _, err := nodes.ListKubemarkNodesForRun(ctx, clients.clientset, cfg.DaemonSetName)
		if err != nil {
			logWarn(fmt.Sprintf("Performance test call %d failed: %v", i+1, err))
		}
	}
	elapsed := time.Since(start)
	avgMs := float64(elapsed.Milliseconds()) / float64(cfg.PerfTests)

	logPerf("Performance Results:")
	logPerf(fmt.Sprintf("  Total time: %v", elapsed))
	logPerf(fmt.Sprintf("  Average per call: %.2fms", avgMs))
}

func cleanupResources(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) error {
	logInfo("Starting comprehensive cleanup of test resources...")

	// Delete daemonset
	daemonsets, err := clients.clientset.AppsV1().DaemonSets(cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=hollow-node"})
	if err != nil {
		logWarn(fmt.Sprintf("Failed to list daemonsets for cleanup: %v", err))
	} else {
		for _, ds := range daemonsets.Items {
			logInfo(fmt.Sprintf("Deleting daemonset %s", ds.Name))
			if err := clients.clientset.AppsV1().DaemonSets(cfg.Namespace).Delete(ctx, ds.Name, metav1.DeleteOptions{}); err != nil {
				logWarn(fmt.Sprintf("Failed to delete daemonset %s: %v", ds.Name, err))
			}
		}
	}

	// Delete hollow nodes
	nodeNames, _, err := nodes.ListKubemarkNodes(ctx, clients.clientset)
	if err != nil {
		logWarn(fmt.Sprintf("Failed to list nodes for cleanup: %v", err))
	} else {
		for _, nodeName := range nodeNames {
			logInfo(fmt.Sprintf("Deleting node %s", nodeName))
			err := clients.clientset.CoreV1().Nodes().Delete(ctx, nodeName, metav1.DeleteOptions{})
			if err != nil {
				logWarn(fmt.Sprintf("Failed to delete node %s: %v", nodeName, err))
			}
		}
	}

	// Delete kubeconfig secret
	logInfo("Deleting kubeconfig secret hollow-node-kubeconfig")
	err = clients.clientset.CoreV1().Secrets(cfg.Namespace).Delete(ctx, "hollow-node-kubeconfig", metav1.DeleteOptions{})
	if err != nil {
		logWarn(fmt.Sprintf("Failed to delete kubeconfig secret: %v", err))
	}

	// Delete service account
	logInfo("Deleting service account hollow-node")
	err = clients.clientset.CoreV1().ServiceAccounts(cfg.Namespace).Delete(ctx, "hollow-node", metav1.DeleteOptions{})
	if err != nil {
		logWarn(fmt.Sprintf("Failed to delete service account: %v", err))
	}

	// Delete cluster role binding
	logInfo("Deleting cluster role binding hollow-node-binding")
	err = clients.clientset.RbacV1().ClusterRoleBindings().Delete(ctx, "hollow-node-binding", metav1.DeleteOptions{})
	if err != nil {
		logWarn(fmt.Sprintf("Failed to delete cluster role binding: %v", err))
	}

	// Optionally delete the namespace if it's empty (only if it's the test namespace)
	if cfg.Namespace == cfgpkg.DefaultNamespace {
		logInfo(fmt.Sprintf("Deleting namespace %s", cfg.Namespace))
		err = clients.clientset.CoreV1().Namespaces().Delete(ctx, cfg.Namespace, metav1.DeleteOptions{})
		if err != nil {
			logWarn(fmt.Sprintf("Failed to delete namespace %s: %v", cfg.Namespace, err))
		}
	}

	logInfo("Comprehensive cleanup completed")
	return nil
}

func ensureNamespace(ctx context.Context, clients *Clients, namespace string) error {
	_, err := clients.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return nil // Namespace exists
	}

	logInfo(fmt.Sprintf("Creating namespace %s", namespace))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	_, err = clients.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating namespace: %w", err)
	}

	return nil
}

func ensureServiceAccount(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) error {
	saName := "hollow-node"

	// Check if ServiceAccount exists
	_, err := clients.clientset.CoreV1().ServiceAccounts(cfg.Namespace).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return nil // ServiceAccount exists
	}

	logInfo(fmt.Sprintf("Creating ServiceAccount %s", saName))
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cfg.Namespace,
		},
	}
	_, err = clients.clientset.CoreV1().ServiceAccounts(cfg.Namespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating ServiceAccount: %w", err)
	}

	// Create ClusterRoleBinding
	crbName := "hollow-node-binding"

	// Check if ClusterRoleBinding exists
	_, err = clients.clientset.RbacV1().ClusterRoleBindings().Get(ctx, crbName, metav1.GetOptions{})
	if err != nil {
		logInfo(fmt.Sprintf("Creating ClusterRoleBinding %s", crbName))
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: crbName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: cfg.Namespace,
			}},
			RoleRef: rbacv1.RoleRef{
				Kind:     "ClusterRole",
				Name:     "system:node",
				APIGroup: "rbac.authorization.k8s.io",
			},
		}
		_, err = clients.clientset.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating ClusterRoleBinding: %w", err)
		}
	}

	return nil
}

func createKubeconfigSecret(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) error {
	secretName := "hollow-node-kubeconfig"

	// Check if secret already exists
	_, err := clients.clientset.CoreV1().Secrets(cfg.Namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err == nil {
		return nil // Secret exists
	}

	logInfo("Creating ServiceAccount token for kubeconfig")

	// Create a token request
	tokenRequest := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences:         cfg.TokenAudiences,
			ExpirationSeconds: func() *int64 { exp := int64(86400 * 30); return &exp }(), // 30 days
		},
	}

	tokenResponse, err := clients.clientset.CoreV1().ServiceAccounts(cfg.Namespace).CreateToken(ctx, "hollow-node", tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating ServiceAccount token: %w", err)
	}

	// Get cluster info
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	restConfig, err := config.ClientConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}

	// Get the internal kubernetes service IP since DNS resolution may not be available
	kubernetesService, err := clients.clientset.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get kubernetes service: %w", err)
	}

	// Use internal kubernetes service IP for hollow nodes
	// This allows hollow nodes to connect from inside the cluster without DNS
	serverHost := fmt.Sprintf("https://%s:443", kubernetesService.Spec.ClusterIP)

	// Create kubeconfig content
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
		base64.StdEncoding.EncodeToString(restConfig.CAData),
		serverHost,
		tokenResponse.Status.Token)

	logInfo(fmt.Sprintf("Creating kubeconfig secret %s", secretName))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cfg.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": []byte(kubeconfigContent),
		},
	}

	_, err = clients.clientset.CoreV1().Secrets(cfg.Namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating kubeconfig secret: %w", err)
	}

	return nil
}

func main() {
	cfg := loadConfigFromFlags()

	logInfo("🎈 Starting kube-inflater - expanding your cluster capacity!")
	logInfo(fmt.Sprintf("Configuration: Timeout=%v ContainersPerPod=%d", cfg.WaitTimeout, cfg.ContainersPerPod))

	clients, err := buildClients(cfg)
	if err != nil {
		logErr(fmt.Sprintf("Failed to build clients: %v", err))
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logInfo("Received shutdown signal")
		cancel()
	}()

	// Cleanup and exit if requested
	if cfg.CleanupOnly {
		err := cleanupResources(ctx, clients, cfg)
		if err != nil {
			logErr(fmt.Sprintf("Cleanup failed: %v", err))
			os.Exit(1)
		}
		return
	}

	// Ensure prerequisites
	if err := ensureNamespace(ctx, clients, cfg.Namespace); err != nil {
		logErr(fmt.Sprintf("Failed to ensure namespace: %v", err))
		os.Exit(1)
	}

	if err := ensureServiceAccount(ctx, clients, cfg); err != nil {
		logErr(fmt.Sprintf("Failed to ensure ServiceAccount: %v", err))
		os.Exit(1)
	}

	if err := createKubeconfigSecret(ctx, clients, cfg); err != nil {
		logErr(fmt.Sprintf("Failed to create kubeconfig secret: %v", err))
		os.Exit(1)
	}

	// With a DaemonSet each eligible node schedules a pod. Each pod creates ContainersPerPod hollow nodes.
	if err := createDaemonSet(ctx, clients, cfg); err != nil {
		logErr(fmt.Sprintf("Failed to create daemonset: %v", err))
		os.Exit(1)
	}

	// Wait for daemonset-derived node count
	if err := waitForNodes(ctx, clients, cfg); err != nil {
		logErr(fmt.Sprintf("Failed waiting for nodes: %v", err))
		os.Exit(1)
	}

	// Run performance tests
	runPerformanceTests(ctx, clients, cfg)

	logInfo("🎈 kube-inflater completed successfully! Cluster fully inflated!")
}
