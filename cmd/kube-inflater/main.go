package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
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
	"kube-inflater/internal/deploymentspec"
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
		NodesToAdd:        getenvInt("NODES_TO_ADD", cfgpkg.DefaultNodesToAdd),
		WaitTimeout:       time.Duration(getenvInt("WAIT_TIMEOUT", cfgpkg.DefaultWaitTimeoutSec)) * time.Second,
		PerfWait:          time.Duration(getenvInt("PERFORMANCE_WAIT", cfgpkg.DefaultPerfWaitSec)) * time.Second,
		PerfTests:         getenvInt("PERFORMANCE_TESTS", cfgpkg.DefaultPerfTests),
		KubemarkImage:     getenvStr("KUBEMARK_IMAGE", cfgpkg.DefaultKubemarkImage),
		NodeStatusFreq:    getenvStr("NODE_STATUS_UPDATE_FREQUENCY", "60s"),
		NodeLeaseDuration: getenvInt("NODE_LEASE_DURATION", 120),
		NodeMonitorGrace:  getenvStr("NODE_MONITOR_GRACE_PERIOD", "240s"),
		Namespace:         cfgpkg.DefaultNamespace,
		TokenAudiences:    cfgpkg.DefaultTokenAudiences,
	}

	timeoutSec := int(cfg.WaitTimeout / time.Second)
	perfWaitSec := int(cfg.PerfWait / time.Second)

	flag.IntVar(&cfg.NodesToAdd, "nodes-to-add", cfg.NodesToAdd, "Number of nodes to add")
	flag.IntVar(&timeoutSec, "timeout", timeoutSec, "Seconds to wait for nodes to become ready")
	flag.IntVar(&perfWaitSec, "perf-wait", perfWaitSec, "Seconds to wait after deployment before measuring performance")
	flag.IntVar(&cfg.PerfTests, "perf-tests", cfg.PerfTests, "Number of API calls to test for performance measurement")
	flag.BoolVar(&cfg.SkipPerfTests, "skip-perf-tests", cfg.SkipPerfTests, "Skip performance tests entirely")
	flag.StringVar(&cfg.NodeStatusFreq, "node-status-frequency", cfg.NodeStatusFreq, "Kubelet node status update frequency")
	flag.IntVar(&cfg.NodeLeaseDuration, "node-lease-duration", cfg.NodeLeaseDuration, "Node lease duration (seconds)")
	flag.StringVar(&cfg.NodeMonitorGrace, "node-monitor-grace", cfg.NodeMonitorGrace, "Node monitor grace period")
	cleanupOnly := flag.Bool("cleanup-only", false, "Only cleanup test resources and exit")
	tokenAudCSV := getenvStr("TOKEN_AUDIENCES", strings.Join(cfg.TokenAudiences, ","))
	tokenAudStr := tokenAudCSV
	flag.StringVar(&tokenAudStr, "token-audiences", tokenAudStr, "Comma-separated ServiceAccount token audiences for hollow-node kubeconfig")
	flag.Parse()

	cfg.WaitTimeout = time.Duration(timeoutSec) * time.Second
	cfg.PerfWait = time.Duration(perfWaitSec) * time.Second

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

	if *cleanupOnly {
		cfg.NodesToAdd = 0
	}

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
func findNextDeploymentNumber(ctx context.Context, clients *Clients, namespace string) (int, error) {
	deployments, err := clients.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=hollow-node",
	})
	if err != nil {
		return 1, fmt.Errorf("listing existing deployments: %w", err)
	}

	maxNum := 0
	for _, deploy := range deployments.Items {
		name := deploy.Name
		if strings.HasPrefix(name, "hollow-nodes-") {
			numStr := strings.TrimPrefix(name, "hollow-nodes-")
			if num, err := strconv.Atoi(numStr); err == nil && num > maxNum {
				maxNum = num
			}
		}
	}

	return maxNum + 1, nil
}

func createDeployment(ctx context.Context, clients *Clients, cfg *cfgpkg.Config, deploymentNumber, replicas int) error {
	deployment := deploymentspec.MakeHollowDeploymentSpec(cfg, deploymentNumber, replicas)

	logInfo(fmt.Sprintf("Creating deployment %s with %d replicas", deployment.Name, replicas))

	_, err := clients.clientset.AppsV1().Deployments(cfg.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating deployment: %w", err)
	}

	logInfo(fmt.Sprintf("Successfully created deployment %s", deployment.Name))
	return nil
}

func waitForNodes(ctx context.Context, clients *Clients, cfg *cfgpkg.Config, expectedCount int) error {
	logInfo(fmt.Sprintf("🎈 Inflating cluster with %d hollow nodes...", expectedCount))

	deadline := time.Now().Add(cfg.WaitTimeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logInfo("Context cancelled, stopping node wait")
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				break
			}

			_, readyCount, err := nodes.ListKubemarkNodes(ctx, clients.clientset)
			if err != nil {
				logWarn(fmt.Sprintf("Failed to list nodes: %v", err))
				continue
			}

			logInfo(fmt.Sprintf("[INFLATE] Ready nodes: %d/%d", readyCount, expectedCount))

			if readyCount >= expectedCount {
				logInfo("🎈 [FULL] Cluster fully inflated! All nodes ready!")
				return nil
			}
		}
	}

	return fmt.Errorf("timeout waiting for nodes to become ready")
}

func runPerformanceTests(ctx context.Context, clients *Clients, cfg *cfgpkg.Config) {
	if cfg.SkipPerfTests {
		logInfo("Skipping performance tests (--skip-perf-tests)")
		return
	}

	logPerf(fmt.Sprintf("Waiting %v before performance tests...", cfg.PerfWait))
	time.Sleep(cfg.PerfWait)

	logPerf(fmt.Sprintf("Running %d performance test calls", cfg.PerfTests))

	// Simple performance measurement - list nodes multiple times
	start := time.Now()
	for i := 0; i < cfg.PerfTests; i++ {
		_, _, err := nodes.ListKubemarkNodes(ctx, clients.clientset)
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

	// Delete deployments
	deployments, err := clients.clientset.AppsV1().Deployments(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=hollow-node",
	})
	if err != nil {
		logWarn(fmt.Sprintf("Failed to list deployments for cleanup: %v", err))
	} else {
		for _, deploy := range deployments.Items {
			logInfo(fmt.Sprintf("Deleting deployment %s", deploy.Name))
			err := clients.clientset.AppsV1().Deployments(cfg.Namespace).Delete(ctx, deploy.Name, metav1.DeleteOptions{})
			if err != nil {
				logWarn(fmt.Sprintf("Failed to delete deployment %s: %v", deploy.Name, err))
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
	logInfo(fmt.Sprintf("Configuration: NodesToAdd=%d, Timeout=%v", cfg.NodesToAdd, cfg.WaitTimeout))

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
	if cfg.NodesToAdd == 0 {
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

	// Find next deployment number
	deploymentNumber, err := findNextDeploymentNumber(ctx, clients, cfg.Namespace)
	if err != nil {
		logErr(fmt.Sprintf("Failed to find next deployment number: %v", err))
		os.Exit(1)
	}

	logInfo(fmt.Sprintf("[PUMP] Will create deployment hollow-nodes-%d with %d replicas", deploymentNumber, cfg.NodesToAdd))

	// Create the deployment
	if err := createDeployment(ctx, clients, cfg, deploymentNumber, cfg.NodesToAdd); err != nil {
		logErr(fmt.Sprintf("Failed to create deployment: %v", err))
		os.Exit(1)
	}

	// Wait for nodes to be ready
	if err := waitForNodes(ctx, clients, cfg, cfg.NodesToAdd); err != nil {
		logErr(fmt.Sprintf("Failed waiting for nodes: %v", err))
		os.Exit(1)
	}

	// Run performance tests
	runPerformanceTests(ctx, clients, cfg)

	logInfo("🎈 kube-inflater completed successfully! Cluster fully inflated!")
}
