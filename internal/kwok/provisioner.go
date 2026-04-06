package kwok

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"kube-inflater/internal/resourcegen"
)

const (
	// KWOKNodeLabel is applied to all KWOK-managed fake nodes.
	KWOKNodeLabel = "type"
	KWOKNodeValue = "kwok"

	// DefaultControllerImage is the default KWOK controller image.
	DefaultControllerImage = "registry.k8s.io/kwok/kwok:v0.7.0"

	// KWOKNamespace is where the KWOK controller runs.
	KWOKNamespace = "kube-system"

	// KWOKDeploymentName is the name of the KWOK controller deployment.
	KWOKDeploymentName = "kwok-controller"

	// PodsPerNode is the max number of pods each KWOK fake node can host.
	PodsPerNode = 1000
)

var (
	deployGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	nodeGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	saGVR     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
	crbGVR    = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	crdGVR    = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	stageGVR  = schema.GroupVersionResource{Group: "kwok.x-k8s.io", Version: "v1alpha1", Resource: "stages"}
)

// podReadyTemplate is the official KWOK v0.7.0 pod-ready status template.
const podReadyTemplate = `{{ $now := Now }}
conditions:
- lastTransitionTime: {{ $now | Quote }}
  status: "True"
  type: Initialized
- lastTransitionTime: {{ $now | Quote }}
  status: "True"
  type: Ready
- lastTransitionTime: {{ $now | Quote }}
  status: "True"
  type: ContainersReady
containerStatuses:
{{ range .spec.containers }}
- image: {{ .image | Quote }}
  name: {{ .name | Quote }}
  ready: true
  restartCount: 0
  state:
    running:
      startedAt: {{ $now | Quote }}
{{ end }}
initContainerStatuses:
{{ range .spec.initContainers }}
- image: {{ .image | Quote }}
  name: {{ .name | Quote }}
  ready: true
  restartCount: 0
  state:
    terminated:
      exitCode: 0
      finishedAt: {{ $now | Quote }}
      reason: Completed
      startedAt: {{ $now | Quote }}
{{ end }}
hostIP: {{ NodeIPWith .spec.nodeName | Quote }}
podIP: {{ PodIPWith .spec.nodeName ( or .spec.hostNetwork false ) ( or .metadata.uid "" ) ( or .metadata.name "" ) ( or .metadata.namespace "" ) | Quote }}
phase: Running
startTime: {{ $now | Quote }}
`

// podCompleteTemplate is the official KWOK v0.7.0 pod-complete status template (for Job pods).
const podCompleteTemplate = `{{ $now := Now }}
{{ $root := . }}
containerStatuses:
{{ range $index, $item := .spec.containers }}
{{ $origin := index $root.status.containerStatuses $index }}
- image: {{ $item.image | Quote }}
  name: {{ $item.name | Quote }}
  ready: false
  restartCount: 0
  started: false
  state:
    terminated:
      exitCode: 0
      finishedAt: {{ $now | Quote }}
      reason: Completed
      startedAt: {{ $now | Quote }}
{{ end }}
phase: Succeeded
`

// Provisioner manages the KWOK controller and fake node lifecycle.
type Provisioner struct {
	client    kubernetes.Interface
	dynClient dynamic.Interface
	runID     string
	dryRun    bool
}

// NewProvisioner creates a new KWOK provisioner.
func NewProvisioner(client kubernetes.Interface, dynClient dynamic.Interface, runID string, dryRun bool) *Provisioner {
	return &Provisioner{client: client, dynClient: dynClient, runID: runID, dryRun: dryRun}
}

// EnsureController deploys the KWOK controller, Stage CRD, and lifecycle stages.
func (p *Provisioner) EnsureController(ctx context.Context) error {
	_, err := p.dynClient.Resource(deployGVR).Namespace(KWOKNamespace).Get(ctx, KWOKDeploymentName, metav1.GetOptions{})
	if err == nil {
		logInfo("KWOK controller already running")
		return nil
	}

	if p.dryRun {
		logInfo("[DRY-RUN] Would deploy KWOK controller with Stage CRDs")
		return nil
	}

	logInfo("Deploying KWOK controller...")

	if err := p.ensureStageCRD(ctx); err != nil {
		return err
	}
	if err := p.ensureStages(ctx); err != nil {
		return err
	}
	if err := p.ensureServiceAccount(ctx); err != nil {
		return err
	}
	if err := p.ensureRBAC(ctx); err != nil {
		return err
	}

	replicas := int64(1)
	deploy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      KWOKDeploymentName,
				"namespace": KWOKNamespace,
				"labels": map[string]interface{}{
					"app": KWOKDeploymentName,
				},
			},
			"spec": map[string]interface{}{
				"replicas": replicas,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": KWOKDeploymentName,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": KWOKDeploymentName,
						},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "kwok-controller",
								"image": DefaultControllerImage,
								"args": []interface{}{
									"--manage-all-nodes=false",
									"--manage-nodes-with-annotation-selector=kwok.x-k8s.io/node=fake",
									"--enable-crds=Stage",
									"--cidr=10.0.0.1/24",
									"--node-ip=10.0.0.1",
									"--node-lease-duration-seconds=40",
								},
								"resources": map[string]interface{}{
									"requests": map[string]interface{}{
										"cpu":    "100m",
										"memory": "256Mi",
									},
									"limits": map[string]interface{}{
										"cpu":    "500m",
										"memory": "512Mi",
									},
								},
							},
						},
						"serviceAccountName": KWOKDeploymentName,
						"restartPolicy":      "Always",
					},
				},
			},
		},
	}

	if _, err := p.dynClient.Resource(deployGVR).Namespace(KWOKNamespace).Create(ctx, deploy, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("deploying KWOK controller: %w", err)
	}

	logInfo("Waiting for KWOK controller to be ready...")
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		dep, err := p.dynClient.Resource(deployGVR).Namespace(KWOKNamespace).Get(ctx, KWOKDeploymentName, metav1.GetOptions{})
		if err == nil {
			ready, _, _ := unstructured.NestedInt64(dep.Object, "status", "readyReplicas")
			if ready >= 1 {
				logInfo("KWOK controller is ready")
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	logWarn("KWOK controller may not be fully ready yet, proceeding")
	return nil
}

// ensureStageCRD creates the kwok.x-k8s.io Stage CRD.
func (p *Provisioner) ensureStageCRD(ctx context.Context) error {
	crd := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": "stages.kwok.x-k8s.io",
			},
			"spec": map[string]interface{}{
				"group": "kwok.x-k8s.io",
				"names": map[string]interface{}{
					"kind":     "Stage",
					"listKind": "StageList",
					"plural":   "stages",
					"singular": "stage",
				},
				"scope": "Cluster",
				"versions": []interface{}{
					map[string]interface{}{
						"name":    "v1alpha1",
						"served":  true,
						"storage": true,
						"schema": map[string]interface{}{
							"openAPIV3Schema": map[string]interface{}{
								"type":                                 "object",
								"x-kubernetes-preserve-unknown-fields": true,
							},
						},
					},
				},
			},
		},
	}
	_, err := p.dynClient.Resource(crdGVR).Create(ctx, crd, metav1.CreateOptions{})
	if err != nil {
		// Already exists is fine.
		if _, getErr := p.dynClient.Resource(crdGVR).Get(ctx, "stages.kwok.x-k8s.io", metav1.GetOptions{}); getErr != nil {
			return fmt.Errorf("creating Stage CRD: %w", err)
		}
	}
	// Wait for CRD to be established.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		obj, err := p.dynClient.Resource(crdGVR).Get(ctx, "stages.kwok.x-k8s.io", metav1.GetOptions{})
		if err == nil {
			conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
			for _, c := range conditions {
				cm, _ := c.(map[string]interface{})
				if cm["type"] == "Established" && cm["status"] == "True" {
					logInfo("Stage CRD established")
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	logWarn("Stage CRD may not be fully established yet")
	return nil
}

// ensureStages creates the KWOK lifecycle stages for nodes and pods.
func (p *Provisioner) ensureStages(ctx context.Context) error {
	stages := []struct {
		name string
		spec map[string]interface{}
	}{
		{
			name: "node-initialize",
			spec: map[string]interface{}{
				"resourceRef": map[string]interface{}{
					"apiGroup": "v1",
					"kind":     "Node",
				},
				"selector": map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      `.status.conditions.[] | select( .type == "Ready" ) | .status`,
							"operator": "NotIn",
							"values":   []interface{}{"True"},
						},
					},
				},
				"delay": map[string]interface{}{
					"durationMilliseconds":       int64(0),
					"jitterDurationMilliseconds": int64(0),
				},
				"next": map[string]interface{}{
					"statusTemplate": "{{ $now := Now }}\nconditions:\n- lastHeartbeatTime: {{ $now | Quote }}\n  lastTransitionTime: {{ $now | Quote }}\n  message: kubelet is posting ready status\n  reason: KubeletReady\n  status: \"True\"\n  type: Ready\n",
				},
			},
		},
		{
			name: "node-heartbeat-with-lease",
			spec: map[string]interface{}{
				"resourceRef": map[string]interface{}{
					"apiGroup": "v1",
					"kind":     "Node",
				},
				"selector": map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      `.status.conditions.[] | select( .type == "Ready" ) | .status`,
							"operator": "In",
							"values":   []interface{}{"True"},
						},
					},
				},
				"delay": map[string]interface{}{
					"durationMilliseconds":       int64(20000),
					"jitterDurationMilliseconds": int64(5000),
				},
				"next": map[string]interface{}{
					"statusTemplate": "{{ $now := Now }}\nconditions:\n- lastHeartbeatTime: {{ $now | Quote }}\n  status: \"True\"\n  type: Ready\n  reason: KubeletReady\n  message: kubelet is posting ready status\n",
				},
			},
		},
		{
			name: "pod-ready",
			spec: map[string]interface{}{
				"resourceRef": map[string]interface{}{
					"apiGroup": "v1",
					"kind":     "Pod",
				},
				"selector": map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      ".metadata.deletionTimestamp",
							"operator": "DoesNotExist",
						},
						map[string]interface{}{
							"key":      ".status.podIP",
							"operator": "DoesNotExist",
						},
					},
				},
				"next": map[string]interface{}{
					"statusTemplate": podReadyTemplate,
				},
			},
		},
		{
			name: "pod-complete",
			spec: map[string]interface{}{
				"resourceRef": map[string]interface{}{
					"apiGroup": "v1",
					"kind":     "Pod",
				},
				"selector": map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      ".metadata.deletionTimestamp",
							"operator": "DoesNotExist",
						},
						map[string]interface{}{
							"key":      ".status.phase",
							"operator": "In",
							"values":   []interface{}{"Running"},
						},
						map[string]interface{}{
							"key":      ".metadata.ownerReferences.[].kind",
							"operator": "In",
							"values":   []interface{}{"Job"},
						},
					},
				},
				"next": map[string]interface{}{
					"statusTemplate": podCompleteTemplate,
				},
			},
		},
		{
			name: "pod-delete",
			spec: map[string]interface{}{
				"resourceRef": map[string]interface{}{
					"apiGroup": "v1",
					"kind":     "Pod",
				},
				"selector": map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      ".metadata.deletionTimestamp",
							"operator": "Exists",
						},
					},
				},
				"next": map[string]interface{}{
					"finalizers": map[string]interface{}{
						"empty": true,
					},
					"delete": true,
				},
			},
		},
	}

	for _, s := range stages {
		stage := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "kwok.x-k8s.io/v1alpha1",
				"kind":       "Stage",
				"metadata": map[string]interface{}{
					"name": s.name,
				},
				"spec": s.spec,
			},
		}
		_, err := p.dynClient.Resource(stageGVR).Create(ctx, stage, metav1.CreateOptions{})
		if err != nil {
			if _, getErr := p.dynClient.Resource(stageGVR).Get(ctx, s.name, metav1.GetOptions{}); getErr != nil {
				return fmt.Errorf("creating stage %s: %w", s.name, err)
			}
		}
	}
	logInfo("KWOK lifecycle stages created (node-initialize, node-heartbeat, pod-ready, pod-complete, pod-delete)")
	return nil
}

func (p *Provisioner) ensureServiceAccount(ctx context.Context) error {
sa := &unstructured.Unstructured{
Object: map[string]interface{}{
"apiVersion": "v1",
"kind":       "ServiceAccount",
"metadata": map[string]interface{}{
"name":      KWOKDeploymentName,
"namespace": KWOKNamespace,
},
},
}
_, err := p.dynClient.Resource(saGVR).Namespace(KWOKNamespace).Create(ctx, sa, metav1.CreateOptions{})
if err != nil {
if _, getErr := p.dynClient.Resource(saGVR).Namespace(KWOKNamespace).Get(ctx, KWOKDeploymentName, metav1.GetOptions{}); getErr != nil {
return fmt.Errorf("creating KWOK service account: %w", err)
}
}
return nil
}

func (p *Provisioner) ensureRBAC(ctx context.Context) error {
crb := &unstructured.Unstructured{
Object: map[string]interface{}{
"apiVersion": "rbac.authorization.k8s.io/v1",
"kind":       "ClusterRoleBinding",
"metadata": map[string]interface{}{
"name": "kwok-controller",
},
"roleRef": map[string]interface{}{
"apiGroup": "rbac.authorization.k8s.io",
"kind":     "ClusterRole",
"name":     "cluster-admin",
},
"subjects": []interface{}{
map[string]interface{}{
"kind":      "ServiceAccount",
"name":      KWOKDeploymentName,
"namespace": KWOKNamespace,
},
},
},
}
_, err := p.dynClient.Resource(crbGVR).Create(ctx, crb, metav1.CreateOptions{})
if err != nil {
if _, getErr := p.dynClient.Resource(crbGVR).Get(ctx, "kwok-controller", metav1.GetOptions{}); getErr != nil {
return fmt.Errorf("creating KWOK RBAC: %w", err)
}
}
return nil
}

// CreateFakeNodes provisions KWOK-managed fake nodes.
func (p *Provisioner) CreateFakeNodes(ctx context.Context, count int) error {
if count <= 0 {
count = 1
}
logInfo(fmt.Sprintf("Creating %d KWOK fake node(s)...", count))

for i := 0; i < count; i++ {
name := fmt.Sprintf("kwok-node-%s-%d", p.runID, i)
if p.dryRun {
logInfo(fmt.Sprintf("[DRY-RUN] Would create KWOK node %s", name))
continue
}

node := &unstructured.Unstructured{
Object: map[string]interface{}{
"apiVersion": "v1",
"kind":       "Node",
"metadata": map[string]interface{}{
"name": name,
"annotations": map[string]interface{}{
				"kwok.x-k8s.io/node": "fake",
			},
"labels": map[string]interface{}{
KWOKNodeLabel:                 KWOKNodeValue,
"app":                        resourcegen.AppLabel,
resourcegen.RunIDLabel:        p.runID,
resourcegen.ResourceTypeLabel: "kwok-node",
"kubernetes.io/hostname":      name,
"kubernetes.io/os":            "linux",
"kubernetes.io/arch":          "amd64",
},
},
"spec": map[string]interface{}{
"taints": []interface{}{
map[string]interface{}{
"key":    KWOKNodeLabel,
"value":  KWOKNodeValue,
"effect": "NoSchedule",
},
},
},
"status": map[string]interface{}{
"allocatable": map[string]interface{}{
"cpu":    "32",
"memory": "256Gi",
"pods":   "1000",
},
"capacity": map[string]interface{}{
"cpu":    "32",
"memory": "256Gi",
"pods":   "1000",
},
"conditions": []interface{}{
map[string]interface{}{
"type":               "Ready",
"status":             "True",
"reason":             "KWOKNodeReady",
"lastHeartbeatTime":  time.Now().UTC().Format(time.RFC3339),
"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
},
},
},
},
}

if _, err := p.dynClient.Resource(nodeGVR).Create(ctx, node, metav1.CreateOptions{}); err != nil {
if _, getErr := p.dynClient.Resource(nodeGVR).Get(ctx, name, metav1.GetOptions{}); getErr != nil {
return fmt.Errorf("creating KWOK node %s: %w", name, err)
}
}
}
logInfo("KWOK fake nodes ready")
return nil
}

// Cleanup removes KWOK fake nodes and optionally the controller.
func (p *Provisioner) Cleanup(ctx context.Context, removeController bool) error {
logInfo("Cleaning up KWOK fake nodes...")

labelSelector := fmt.Sprintf("%s=%s,app=%s", KWOKNodeLabel, KWOKNodeValue, resourcegen.AppLabel)
if p.runID != "" {
labelSelector += fmt.Sprintf(",%s=%s", resourcegen.RunIDLabel, p.runID)
}

list, err := p.dynClient.Resource(nodeGVR).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
if err != nil {
return fmt.Errorf("listing KWOK nodes: %w", err)
}

for _, node := range list.Items {
if p.dryRun {
logInfo(fmt.Sprintf("[DRY-RUN] Would delete KWOK node %s", node.GetName()))
continue
}
if err := p.dynClient.Resource(nodeGVR).Delete(ctx, node.GetName(), metav1.DeleteOptions{}); err != nil {
logWarn(fmt.Sprintf("Failed deleting KWOK node %s: %v", node.GetName(), err))
}
}
logInfo(fmt.Sprintf("Deleted %d KWOK node(s)", len(list.Items)))

if removeController {
	logInfo("Removing KWOK controller and stages...")
	_ = p.dynClient.Resource(deployGVR).Namespace(KWOKNamespace).Delete(ctx, KWOKDeploymentName, metav1.DeleteOptions{})
	_ = p.dynClient.Resource(saGVR).Namespace(KWOKNamespace).Delete(ctx, KWOKDeploymentName, metav1.DeleteOptions{})
	_ = p.dynClient.Resource(crbGVR).Delete(ctx, "kwok-controller", metav1.DeleteOptions{})
	for _, name := range []string{"node-initialize", "node-heartbeat-with-lease", "pod-ready", "pod-complete", "pod-delete"} {
		_ = p.dynClient.Resource(stageGVR).Delete(ctx, name, metav1.DeleteOptions{})
	}
	_ = p.dynClient.Resource(crdGVR).Delete(ctx, "stages.kwok.x-k8s.io", metav1.DeleteOptions{})
}

return nil
}

// NodesNeeded calculates the number of KWOK nodes needed for the given pod count.
func NodesNeeded(podCount int) int {
	n := (podCount + PodsPerNode - 1) / PodsPerNode
	if n < 1 {
		n = 1
	}
	return n
}

func logInfo(msg string) { fmt.Printf("[INFO] %s\n", msg) }
func logWarn(msg string) { fmt.Printf("[WARN] %s\n", msg) }
