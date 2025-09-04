package deploymentspec

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cfgpkg "kube-inflater/internal/config"
)

// MakeHollowDeploymentSpec builds a Deployment for hollow nodes
func MakeHollowDeploymentSpec(cfg *cfgpkg.Config, deploymentNumber int, replicas int) *appsv1.Deployment {
	deploymentName := fmt.Sprintf("hollow-nodes-%d", deploymentNumber)
	replicasInt32 := int32(replicas)

	labels := map[string]string{
		"app":        "hollow-node",
		"deployment": fmt.Sprintf("deployment-%d", deploymentNumber),
	}

	// Pod template labels
	podLabels := map[string]string{
		"app":        "hollow-node",
		"deployment": fmt.Sprintf("deployment-%d", deploymentNumber),
	}

	vols := []corev1.Volume{
		{
			Name: "kubeconfig-volume",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "hollow-node-kubeconfig",
				},
			},
		},
		{
			Name: "logs-volume",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	kubeconfigMounts := []corev1.VolumeMount{
		{Name: "kubeconfig-volume", MountPath: "/kubeconfig", ReadOnly: true},
		{Name: "logs-volume", MountPath: "/var/log"},
	}

	// Generate secret name from image registry
	secretName := "acrvapa22-secret" // default fallback
	if strings.Contains(cfg.KubemarkImage, "k3sacr1.azurecr.io") {
		secretName = "" // k3sacr1 has anonymous pull enabled, no secret needed
	} else if strings.Contains(cfg.KubemarkImage, "acrvapa23.azurecr.io") {
		secretName = "acrvapa23-secret"
	}

	// Environment variables for dynamic node naming
	dynamicEnv := []corev1.EnvVar{
		{Name: "DEPLOYMENT_NUMBER", Value: fmt.Sprintf("%d", deploymentNumber)},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
	}

	// Create multiple containers per pod based on configuration
	var containers []corev1.Container
	for i := 0; i < cfg.ContainersPerPod; i++ {
		containerSuffix := fmt.Sprintf("-%d", i)
		
		// Kubelet container for this node
		kubeletContainer := corev1.Container{
			Name:    fmt.Sprintf("hollow-kubelet%s", containerSuffix),
			Image:   cfg.KubemarkImage,
			Env:     append(dynamicEnv, corev1.EnvVar{Name: "CONTAINER_INDEX", Value: fmt.Sprintf("%d", i)}),
			Command: []string{"/go-runner"},
			Args: []string{
				fmt.Sprintf("-log-file=/var/log/kubelet-$(POD_NAME)%s.log", containerSuffix),
				"-also-stdout=true",
				"/kubemark",
				"--morph=kubelet",
				fmt.Sprintf("--name=$(POD_NAME)%s", containerSuffix), // Unique node name per container
				"--kubeconfig=/kubeconfig/kubeconfig",
				fmt.Sprintf("--node-labels=kubemark=true,incremental-test=true,deployment=deployment-%d,container-index=%d", deploymentNumber, i),
				"--max-pods=110",
				"--use-host-image-service=false",
				fmt.Sprintf("--node-lease-duration-seconds=%d", cfg.NodeLeaseDuration),
				"--node-status-update-frequency=" + cfg.NodeStatusFreq,
				"--node-status-report-frequency=15m",
				fmt.Sprintf("--kubelet-port=%d", 10250+i*2),           // Unique port per container
				fmt.Sprintf("--kubelet-read-only-port=%d", 10255+i*2), // Unique read-only port per container
				"--v=4",
			},
			VolumeMounts: kubeconfigMounts,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20m"),
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
		}

		// Proxy container for this node
		proxyContainer := corev1.Container{
			Name:    fmt.Sprintf("hollow-proxy%s", containerSuffix),
			Image:   cfg.KubemarkImage,
			Env:     append(dynamicEnv, corev1.EnvVar{Name: "CONTAINER_INDEX", Value: fmt.Sprintf("%d", i)}),
			Command: []string{"/go-runner"},
			Args: []string{
				fmt.Sprintf("-log-file=/var/log/kubeproxy-$(POD_NAME)%s.log", containerSuffix),
				"-also-stdout=true",
				"/kubemark",
				"--morph=proxy",
				fmt.Sprintf("--name=$(POD_NAME)%s", containerSuffix), // Unique node name per container
				"--kubeconfig=/kubeconfig/kubeconfig",
				"--v=4",
			},
			VolumeMounts: kubeconfigMounts,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
		}

		containers = append(containers, kubeletContainer, proxyContainer)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: cfg.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicasInt32,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					ImagePullSecrets: func() []corev1.LocalObjectReference {
						if secretName != "" {
							return []corev1.LocalObjectReference{{Name: secretName}}
						}
						return nil
					}(),
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.NodeSelectorOpDoesNotExist},
										{Key: "kubemark", Operator: corev1.NodeSelectorOpDoesNotExist},
										{Key: "node-role.kubernetes.io/worker", Operator: corev1.NodeSelectorOpExists},
										{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"k3s"}},
									},
								}},
							},
						},
						PodAntiAffinity: &corev1.PodAntiAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"app": "hollow-node"},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							}},
						},
					},
					Volumes:       vols,
					Containers:    containers,
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}

	return deployment
}
