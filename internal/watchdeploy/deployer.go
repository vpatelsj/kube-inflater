package watchdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"kube-inflater/internal/watchstress"
)

const (
	DefaultImage       = "k3sacr1.azurecr.io/watch-agent:latest"
	Namespace          = "watch-stress"
	ServiceAccountName = "watch-agent"
	ConnsPerPod        = 15 // connections per watch-agent pod
)

// Config holds watch deployment parameters.
type Config struct {
	Image            string
	Connections      int
	Stagger          time.Duration
	WatchTypes       []string
	StressNamespace  string // namespace prefix for watched resources
	SpreadCount      int
	MutatorRate      int
	MutatorBatchSize int
	DataSizeBytes    int
	RunID            string
	DryRun           bool
}

// Result holds collected metrics from the watch-agent Jobs.
type Result struct {
	WatchSummary   *watchstress.MetricsSummary
	MutatorSummary *watchstress.MutatorSummary
}

// Deployer manages watch-agent Job lifecycle in-cluster.
type Deployer struct {
	client kubernetes.Interface
	cfg    Config
}

// NewDeployer creates a new watch-agent deployer.
func NewDeployer(client kubernetes.Interface, cfg Config) *Deployer {
	if cfg.Image == "" {
		cfg.Image = DefaultImage
	}
	if cfg.StressNamespace == "" {
		cfg.StressNamespace = "stress-test"
	}
	if cfg.SpreadCount == 0 {
		cfg.SpreadCount = 10
	}
	if cfg.MutatorBatchSize == 0 {
		cfg.MutatorBatchSize = 10
	}
	return &Deployer{client: client, cfg: cfg}
}

// Deploy sets up RBAC, FlowSchema, and launches watch+mutator Jobs.
// It returns after the Jobs are created (does not wait for completion).
func (d *Deployer) Deploy(ctx context.Context) error {
	if d.cfg.DryRun {
		logInfo("[DRY-RUN] Would deploy watch-agent Jobs")
		return nil
	}

	if err := d.ensureRBAC(ctx); err != nil {
		return fmt.Errorf("RBAC setup: %w", err)
	}

	if err := d.ensureFlowSchema(ctx); err != nil {
		// Non-fatal: FlowSchema might not be supported
		logWarn(fmt.Sprintf("FlowSchema setup: %v (non-fatal)", err))
	}

	replicas := d.cfg.Connections / ConnsPerPod
	if d.cfg.Connections%ConnsPerPod != 0 {
		replicas++
	}
	if replicas < 1 {
		replicas = 1
	}

	logInfo(fmt.Sprintf("Deploying %d watch-agent pods (%d conns each = %d total) + 1 mutator",
		replicas, ConnsPerPod, replicas*ConnsPerPod))

	// Mutator duration = 0 means run forever (until Job is deleted)
	// We give it a very long deadline so it outlasts the watcher
	mutatorDuration := 86400 // 24h — will be killed when we delete the Job

	if err := d.createMutatorJob(ctx, mutatorDuration); err != nil {
		return fmt.Errorf("mutator job: %w", err)
	}

	// Wait for mutator to be Running before starting watchers
	if err := d.waitForMutatorRunning(ctx, 60*time.Second); err != nil {
		logWarn(fmt.Sprintf("Mutator may not be ready: %v", err))
	}

	if err := d.createWatcherJob(ctx, replicas); err != nil {
		return fmt.Errorf("watcher job: %w", err)
	}

	return nil
}

// WaitAndCollect stops the watch-agent Jobs and collects results from ConfigMaps.
// Suspending a Job sends SIGTERM to all active pods. Pods write results to
// ConfigMaps during their graceful shutdown, then exit.
func (d *Deployer) WaitAndCollect(ctx context.Context) (*Result, error) {
	if d.cfg.DryRun {
		return &Result{}, nil
	}

	// Suspend Jobs — this sends SIGTERM to all active pods.
	// Pods handle SIGTERM: flush metrics, write results to ConfigMaps, then exit.
	logInfo("Stopping watch-agent Jobs...")
	d.suspendJob(ctx, "watch-mutator")
	d.suspendJob(ctx, "watch-agent")

	// Wait for result ConfigMaps to be written by terminating pods
	expectedWatchers := d.cfg.Connections / ConnsPerPod
	if d.cfg.Connections%ConnsPerPod != 0 {
		expectedWatchers++
	}
	if expectedWatchers < 1 {
		expectedWatchers = 1
	}
	expectedTotal := expectedWatchers + 1 // +1 for mutator
	logInfo(fmt.Sprintf("Waiting for %d result ConfigMaps (%d watchers + 1 mutator)...", expectedTotal, expectedWatchers))
	collected := d.waitForConfigMaps(ctx, expectedTotal, 3*time.Minute)
	logInfo(fmt.Sprintf("Collected %d/%d result ConfigMaps", collected, expectedTotal))

	// Collect and aggregate results
	watchSummary, err := d.collectWatcherResults(ctx)
	if err != nil {
		logWarn(fmt.Sprintf("Failed collecting watcher results: %v", err))
	}

	mutatorSummary, err := d.collectMutatorResults(ctx)
	if err != nil {
		logWarn(fmt.Sprintf("Failed collecting mutator results: %v", err))
	}

	// Delete Jobs and result ConfigMaps
	d.deleteJobs(ctx)

	return &Result{
		WatchSummary:   watchSummary,
		MutatorSummary: mutatorSummary,
	}, nil
}

// Cleanup removes all watch-agent resources: Jobs, ConfigMaps, RBAC, FlowSchema, namespace.
func (d *Deployer) Cleanup(ctx context.Context) error {
	if d.cfg.DryRun {
		logInfo("[DRY-RUN] Would clean up watch-agent resources")
		return nil
	}

	logInfo("Cleaning up watch-agent resources...")

	// Delete Jobs
	d.deleteJobs(ctx)

	// Delete result ConfigMaps
	d.client.CoreV1().ConfigMaps(Namespace).DeleteCollection(ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "app=watch-agent"})

	// Delete FlowSchema
	d.client.FlowcontrolV1().FlowSchemas().Delete(ctx, "watch-agent-exempt", metav1.DeleteOptions{})

	// Delete ClusterRoleBinding and ClusterRole
	d.client.RbacV1().ClusterRoleBindings().Delete(ctx, "watch-agent", metav1.DeleteOptions{})
	d.client.RbacV1().ClusterRoles().Delete(ctx, "watch-agent", metav1.DeleteOptions{})

	// Delete the namespace (cascades everything else)
	propagation := metav1.DeletePropagationForeground
	d.client.CoreV1().Namespaces().Delete(ctx, Namespace, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})

	// Wait for namespace deletion
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		_, err := d.client.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			logInfo("Watch-agent namespace deleted")
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	logInfo("Watch-agent cleanup completed")
	return nil
}

// --- RBAC ---

func (d *Deployer) ensureRBAC(ctx context.Context) error {
	// Namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: Namespace},
	}
	if _, err := d.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	// ServiceAccount
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: ServiceAccountName, Namespace: Namespace},
	}
	if _, err := d.client.CoreV1().ServiceAccounts(Namespace).Create(ctx, sa, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	// ClusterRole
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-agent"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"stresstest.kube-inflater.io"},
				Resources: []string{"stressitems"},
				Verbs:     []string{"list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps", "secrets", "services", "namespaces", "pods", "serviceaccounts"},
				Verbs:     []string{"list", "watch"},
			},
			{
				APIGroups: []string{"batch"},
				Resources: []string{"jobs"},
				Verbs:     []string{"list", "watch"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"statefulsets"},
				Verbs:     []string{"list", "watch"},
			},
		},
	}
	if _, err := d.client.RbacV1().ClusterRoles().Create(ctx, cr, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	// ClusterRoleBinding
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-agent"},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      ServiceAccountName,
			Namespace: Namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "watch-agent",
		},
	}
	if _, err := d.client.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	// Role for writing result ConfigMaps
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-agent-results", Namespace: Namespace},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"create", "update", "patch"},
			},
		},
	}
	if _, err := d.client.RbacV1().Roles(Namespace).Create(ctx, role, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	// RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-agent-results", Namespace: Namespace},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      ServiceAccountName,
			Namespace: Namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "watch-agent-results",
		},
	}
	if _, err := d.client.RbacV1().RoleBindings(Namespace).Create(ctx, rb, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (d *Deployer) ensureFlowSchema(ctx context.Context) error {
	fs := &flowcontrolv1.FlowSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-agent-exempt"},
		Spec: flowcontrolv1.FlowSchemaSpec{
			MatchingPrecedence: 100,
			PriorityLevelConfiguration: flowcontrolv1.PriorityLevelConfigurationReference{
				Name: "exempt",
			},
			Rules: []flowcontrolv1.PolicyRulesWithSubjects{
				{
					Subjects: []flowcontrolv1.Subject{
						{
							Kind: flowcontrolv1.SubjectKindServiceAccount,
							ServiceAccount: &flowcontrolv1.ServiceAccountSubject{
								Name:      ServiceAccountName,
								Namespace: Namespace,
							},
						},
					},
					ResourceRules: []flowcontrolv1.ResourcePolicyRule{
						{
							APIGroups:    []string{"*"},
							Resources:    []string{"*"},
							Namespaces:   []string{"*"},
							Verbs:        []string{"*"},
							ClusterScope: true,
						},
					},
				},
			},
		},
	}
	if _, err := d.client.FlowcontrolV1().FlowSchemas().Create(ctx, fs, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// --- Jobs ---

func (d *Deployer) createWatcherJob(ctx context.Context, replicas int) error {
	replicas32 := int32(replicas)
	backoff := int32(10000)
	ttl := int32(600)
	terminationGrace := int64(120)

	resourceType := "customresources"
	if len(d.cfg.WatchTypes) > 0 {
		resourceType = strings.Join(d.cfg.WatchTypes, ",")
	}

	// Duration=0 → watches run until the Job is deleted.
	// We set a very long duration (24h) and kill the Job when resource creation finishes.
	duration := 86400

	stagger := int(d.cfg.Stagger.Seconds())

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-agent",
			Namespace: Namespace,
		},
		Spec: batchv1.JobSpec{
			Parallelism:             &replicas32,
			Completions:             &replicas32,
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":    "watch-agent",
						"run-id": d.cfg.RunID,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            ServiceAccountName,
					TerminationGracePeriodSeconds: &terminationGrace,
					RestartPolicy:                 corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "watch-agent",
							Image: d.cfg.Image,
							Args: []string{
								fmt.Sprintf("--connections=%d", ConnsPerPod),
								fmt.Sprintf("--duration=%d", duration),
								fmt.Sprintf("--stagger=%d", stagger),
								fmt.Sprintf("--resource-type=%s", resourceType),
								fmt.Sprintf("--namespace=%s", d.cfg.StressNamespace),
								fmt.Sprintf("--spread-count=%d", d.cfg.SpreadCount),
								"--qps=1000000",
								"--burst=1000000",
							},
							Env: []corev1.EnvVar{
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
									},
								},
								{Name: "RUN_ID", Value: d.cfg.RunID},
								{Name: "RESULTS_NAMESPACE", Value: Namespace},
								{Name: "ROLE", Value: "watcher"},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("25m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
		},
	}

	// Delete any existing Job first
	d.client.BatchV1().Jobs(Namespace).Delete(ctx, "watch-agent", metav1.DeleteOptions{})
	time.Sleep(time.Second)

	_, err := d.client.BatchV1().Jobs(Namespace).Create(ctx, job, metav1.CreateOptions{})
	return err
}

func (d *Deployer) createMutatorJob(ctx context.Context, duration int) error {
	parallelism := int32(1)
	completions := int32(1)
	backoff := int32(3)
	ttl := int32(600)
	terminationGrace := int64(30)

	dataSize := d.cfg.DataSizeBytes
	if dataSize == 0 {
		dataSize = 1024
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-mutator",
			Namespace: Namespace,
		},
		Spec: batchv1.JobSpec{
			Parallelism:             &parallelism,
			Completions:             &completions,
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":    "watch-mutator",
						"run-id": d.cfg.RunID,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            ServiceAccountName,
					TerminationGracePeriodSeconds: &terminationGrace,
					RestartPolicy:                 corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "watch-mutator",
							Image: d.cfg.Image,
							Args: []string{
								"mutate",
								fmt.Sprintf("--rate=%d", d.cfg.MutatorRate),
								fmt.Sprintf("--duration=%d", duration),
								fmt.Sprintf("--namespace=%s", d.cfg.StressNamespace),
								fmt.Sprintf("--spread-count=%d", d.cfg.SpreadCount),
								fmt.Sprintf("--data-size=%d", dataSize),
								fmt.Sprintf("--batch-size=%d", d.cfg.MutatorBatchSize),
								"--qps=1000000",
								"--burst=1000000",
							},
							Env: []corev1.EnvVar{
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
									},
								},
								{Name: "RUN_ID", Value: d.cfg.RunID},
								{Name: "RESULTS_NAMESPACE", Value: Namespace},
								{Name: "ROLE", Value: "mutator"},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
		},
	}

	// Delete any existing Job first
	d.client.BatchV1().Jobs(Namespace).Delete(ctx, "watch-mutator", metav1.DeleteOptions{})
	time.Sleep(time.Second)

	_, err := d.client.BatchV1().Jobs(Namespace).Create(ctx, job, metav1.CreateOptions{})
	return err
}

// --- Wait and Collect ---

func (d *Deployer) waitForMutatorRunning(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pods, err := d.client.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=watch-mutator,run-id=%s", d.cfg.RunID),
		})
		if err == nil && len(pods.Items) > 0 {
			if pods.Items[0].Status.Phase == corev1.PodRunning {
				logInfo("Mutator pod running")
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("mutator pod did not reach Running state within %v", timeout)
}

func (d *Deployer) suspendJob(ctx context.Context, name string) {
	patchData := []byte(`{"spec":{"suspend":true}}`)
	_, err := d.client.BatchV1().Jobs(Namespace).Patch(ctx, name, types.MergePatchType, patchData, metav1.PatchOptions{})
	if err != nil && !errors.IsNotFound(err) {
		logWarn(fmt.Sprintf("Failed to suspend job %s: %v", name, err))
	}
}

func (d *Deployer) waitForConfigMaps(ctx context.Context, expected int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			break
		}
		count := d.countAllResultConfigMaps(ctx)
		if count >= expected {
			return count
		}
		logInfo(fmt.Sprintf("  ConfigMaps: %d/%d", count, expected))
		time.Sleep(5 * time.Second)
	}
	return d.countAllResultConfigMaps(ctx)
}

func (d *Deployer) countAllResultConfigMaps(ctx context.Context) int {
	list, err := d.client.CoreV1().ConfigMaps(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("run-id=%s", d.cfg.RunID),
	})
	if err != nil {
		return 0
	}
	return len(list.Items)
}

func (d *Deployer) collectWatcherResults(ctx context.Context) (*watchstress.MetricsSummary, error) {
	list, err := d.client.CoreV1().ConfigMaps(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("role=watcher,run-id=%s", d.cfg.RunID),
	})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("no result ConfigMaps found")
	}

	logInfo(fmt.Sprintf("Aggregating results from %d watch-agent pods...", len(list.Items)))

	var totalEvents, reconnects, errs, peakAlive int64
	var totalConnectMs, totalDeliveryMs, maxConnectMs, maxDeliveryMs, maxP99Ms float64
	var minEPC, maxEPC int64
	var durationSec float64
	first := true

	for _, cm := range list.Items {
		data, ok := cm.Data["result.json"]
		if !ok {
			continue
		}
		var r podResult
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			continue
		}
		totalEvents += r.Events
		reconnects += r.Reconnects
		errs += r.Errors
		peakAlive += r.PeakAliveWatches
		totalConnectMs += r.AvgConnectMs
		totalDeliveryMs += r.AvgDeliveryMs
		if r.MaxConnectMs > maxConnectMs {
			maxConnectMs = r.MaxConnectMs
		}
		if r.MaxDeliveryMs > maxDeliveryMs {
			maxDeliveryMs = r.MaxDeliveryMs
		}
		if r.P99DeliveryMs > maxP99Ms {
			maxP99Ms = r.P99DeliveryMs
		}
		if first || r.MinEventsPerConn < minEPC {
			minEPC = r.MinEventsPerConn
		}
		if r.MaxEventsPerConn > maxEPC {
			maxEPC = r.MaxEventsPerConn
		}
		if r.DurationSec > durationSec {
			durationSec = r.DurationSec
		}
		first = false
	}

	n := float64(len(list.Items))
	eps := float64(0)
	if durationSec > 0 {
		eps = float64(totalEvents) / durationSec
	}

	return &watchstress.MetricsSummary{
		TotalEvents:        totalEvents,
		EventsPerSecond:    eps,
		Reconnects:         reconnects,
		Errors:             errs,
		Duration:           time.Duration(durationSec * float64(time.Second)),
		PeakAliveWatches:   peakAlive,
		AvgConnectLatency:  time.Duration(totalConnectMs / n * float64(time.Millisecond)),
		MaxConnectLatency:  time.Duration(maxConnectMs * float64(time.Millisecond)),
		AvgDeliveryLatency: time.Duration(totalDeliveryMs / n * float64(time.Millisecond)),
		MaxDeliveryLatency: time.Duration(maxDeliveryMs * float64(time.Millisecond)),
		P99DeliveryLatency: time.Duration(maxP99Ms * float64(time.Millisecond)),
		MinEventsPerConn:   minEPC,
		MaxEventsPerConn:   maxEPC,
	}, nil
}

func (d *Deployer) collectMutatorResults(ctx context.Context) (*watchstress.MutatorSummary, error) {
	list, err := d.client.CoreV1().ConfigMaps(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("role=mutator,run-id=%s", d.cfg.RunID),
	})
	if err != nil || len(list.Items) == 0 {
		return nil, fmt.Errorf("no mutator result ConfigMap found")
	}

	data, ok := list.Items[0].Data["result.json"]
	if !ok {
		return nil, fmt.Errorf("no result.json in mutator ConfigMap")
	}

	var result watchstress.MutatorSummary
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return nil, fmt.Errorf("parsing mutator result: %w", err)
	}
	return &result, nil
}

func (d *Deployer) deleteJobs(ctx context.Context) {
	propagation := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &propagation}

	d.client.BatchV1().Jobs(Namespace).Delete(ctx, "watch-agent", opts)
	d.client.BatchV1().Jobs(Namespace).Delete(ctx, "watch-mutator", opts)

	// Delete result ConfigMaps
	d.client.CoreV1().ConfigMaps(Namespace).DeleteCollection(ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: fmt.Sprintf("run-id=%s", d.cfg.RunID)})
}

// --- Helpers ---

type podResult struct {
	Events           int64   `json:"events"`
	EventsPerSec     float64 `json:"events_per_sec"`
	Reconnects       int64   `json:"reconnects"`
	Errors           int64   `json:"errors"`
	PeakAliveWatches int64   `json:"peak_alive_watches"`
	AvgConnectMs     float64 `json:"avg_connect_ms"`
	MaxConnectMs     float64 `json:"max_connect_ms"`
	AvgDeliveryMs    float64 `json:"avg_delivery_ms"`
	MaxDeliveryMs    float64 `json:"max_delivery_ms"`
	P99DeliveryMs    float64 `json:"p99_delivery_ms"`
	MinEventsPerConn int64   `json:"min_events_per_conn"`
	MaxEventsPerConn int64   `json:"max_events_per_conn"`
	DurationSec      float64 `json:"duration_sec"`
	Connections      int     `json:"connections"`
}

func logInfo(msg string) {
	fmt.Printf("[INFO] %s\n", msg)
}

func logWarn(msg string) {
	fmt.Printf("[WARN] %s\n", msg)
}
