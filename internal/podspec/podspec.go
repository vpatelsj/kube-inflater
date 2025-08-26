package podspec

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cfgpkg "kube-inflater/internal/config"
)

// MakeHollowPodSpec builds a Pod for one hollow node
func MakeHollowPodSpec(cfg *cfgpkg.Config, batch int, idx int) *corev1.Pod {
	name := fmt.Sprintf("hollow-node-%d", idx)
	labels := map[string]string{"app": "hollow-node", "batch": fmt.Sprintf("batch-%d", batch), "node-id": name}
	vols := []corev1.Volume{{Name: "kubeconfig-volume", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "hollow-node-kubeconfig"}}}, {Name: "logs-volume", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	mounts := []corev1.VolumeMount{{Name: "kubeconfig-volume", MountPath: "/kubeconfig", ReadOnly: true}, {Name: "logs-volume", MountPath: "/var/log"}}
	env := []corev1.EnvVar{{Name: "NODE_NAME", Value: name}}
	kubeletArgs := []string{"/go-runner", "-log-file=/var/log/kubelet-" + name + ".log", "-also-stdout=true", "/kubemark",
		"--morph=kubelet", "--name=" + name, "--kubeconfig=/kubeconfig/kubeconfig",
		"--node-labels=kubemark=true,incremental-test=true,batch=batch-" + fmt.Sprint(batch),
		"--max-pods=110", "--use-host-image-service=false",
		fmt.Sprintf("--node-lease-duration-seconds=%d", cfg.NodeLeaseDuration),
		"--node-status-update-frequency=" + cfg.NodeStatusFreq,
		"--node-status-report-frequency=15m", "--v=4"}
	proxyArgs := []string{"/go-runner", "-log-file=/var/log/kubeproxy-" + name + ".log", "-also-stdout=true", "/kubemark",
		"--morph=proxy", "--name=" + name, "--kubeconfig=/kubeconfig/kubeconfig", "--v=4"}

	// Generate secret name from image registry
	secretName := "acrvapa22-secret" // default fallback
	if strings.Contains(cfg.KubemarkImage, "k3sacr1.azurecr.io") {
		secretName = "" // k3sacr1 has anonymous pull enabled, no secret needed
	} else if strings.Contains(cfg.KubemarkImage, "acrvapa23.azurecr.io") {
		secretName = "acrvapa23-secret"
	}

	var imagePullSecrets []corev1.LocalObjectReference
	if secretName != "" {
		imagePullSecrets = []corev1.LocalObjectReference{{Name: secretName}}
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace, Labels: labels}, Spec: corev1.PodSpec{
		ImagePullSecrets: imagePullSecrets,
		Affinity: &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{
				{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.NodeSelectorOpDoesNotExist},
				{Key: "kubemark", Operator: corev1.NodeSelectorOpDoesNotExist},
				{Key: "node-role.kubernetes.io/worker", Operator: corev1.NodeSelectorOpExists},
				{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"k3s"}},
			}}}}},
			PodAntiAffinity: &corev1.PodAntiAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{Weight: 100, PodAffinityTerm: corev1.PodAffinityTerm{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hollow-node"}}, TopologyKey: "kubernetes.io/hostname"}}}},
		},
		Volumes: vols,
		Containers: []corev1.Container{
			{Name: "hollow-kubelet", Image: cfg.KubemarkImage, Env: env, Command: kubeletArgs, VolumeMounts: mounts,
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20m"), corev1.ResourceMemory: resource.MustParse("50Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("200Mi")}}},
			{Name: "hollow-proxy", Image: cfg.KubemarkImage, Env: env, Command: proxyArgs, VolumeMounts: mounts,
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("25Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("100Mi")}}},
		},
		RestartPolicy: corev1.RestartPolicyAlways,
	}}
	return pod
}
