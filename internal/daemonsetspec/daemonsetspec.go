package daemonsetspec

import (
    "fmt"
    "strings"

    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/resource"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    cfgpkg "kube-inflater/internal/config"
)

// MakeHollowDaemonSetSpec builds a DaemonSet running hollow kubelet/proxy containers.
// Each pod spawns `containersPerPod` hollow nodes (each node = kubelet+proxy container pair).
func MakeHollowDaemonSetSpec(cfg *cfgpkg.Config, containersPerPod int) *appsv1.DaemonSet {
    name := "hollow-nodes"

    labels := map[string]string{
        "app": "hollow-node",
        "kind": "daemonset",
    }

    podLabels := map[string]string{
        "app": "hollow-node",
        "kind": "daemonset",
    }

    vols := []corev1.Volume{
        {
            Name: "kubeconfig-volume",
            VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "hollow-node-kubeconfig"}},
        },
        {
            Name:         "logs-volume",
            VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
        },
    }

    kubeconfigMounts := []corev1.VolumeMount{
        {Name: "kubeconfig-volume", MountPath: "/kubeconfig", ReadOnly: true},
        {Name: "logs-volume", MountPath: "/var/log"},
    }

    // Generate imagePullSecret name if needed based on registry
    secretName := "acrvapa22-secret" // default fallback
    if strings.Contains(cfg.KubemarkImage, "k3sacr1.azurecr.io") {
        secretName = "" // anonymous pull
    } else if strings.Contains(cfg.KubemarkImage, "acrvapa23.azurecr.io") {
        secretName = "acrvapa23-secret"
    }

    // Environment vars common to all containers in a pod
    dynamicEnv := []corev1.EnvVar{{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}}}

    var containers []corev1.Container
    for i := 0; i < containersPerPod; i++ {
        suffix := fmt.Sprintf("-%d", i)

        kubeletArgs := []string{
            fmt.Sprintf("-log-file=/var/log/kubelet-$(POD_NAME)%s.log", suffix),
            "-also-stdout=true",
            "/kubemark",
            "--morph=kubelet",
            fmt.Sprintf("--name=$(POD_NAME)%s", suffix),
            "--kubeconfig=/kubeconfig/kubeconfig",
            fmt.Sprintf("--node-labels=kubemark=true,incremental-test=true,daemonset=true,container-index=%d", i),
            "--max-pods=110",
            "--use-host-image-service=false",
            fmt.Sprintf("--node-lease-duration-seconds=%d", cfg.NodeLeaseDuration),
            "--node-status-update-frequency=" + cfg.NodeStatusFreq,
            "--node-status-report-frequency=15m",
        }
        if i == 0 {
            kubeletArgs = append(kubeletArgs, "--kubelet-read-only-port=0")
        } else {
            kubeletArgs = append(kubeletArgs, "--kubelet-port=0", "--kubelet-read-only-port=0")
        }
        kubeletArgs = append(kubeletArgs, "--v=4")

        kubeletContainer := corev1.Container{
            Name:         fmt.Sprintf("hollow-kubelet%s", suffix),
            Image:        cfg.KubemarkImage,
            Env:          append(append([]corev1.EnvVar{}, dynamicEnv...), corev1.EnvVar{Name: "CONTAINER_INDEX", Value: fmt.Sprintf("%d", i)}),
            Command:      []string{"/go-runner"},
            Args:         kubeletArgs,
            VolumeMounts: kubeconfigMounts,
            Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20m"), corev1.ResourceMemory: resource.MustParse("50Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("200Mi")}},
        }

        proxyContainer := corev1.Container{
            Name:         fmt.Sprintf("hollow-proxy%s", suffix),
            Image:        cfg.KubemarkImage,
            Env:          append(append([]corev1.EnvVar{}, dynamicEnv...), corev1.EnvVar{Name: "CONTAINER_INDEX", Value: fmt.Sprintf("%d", i)}),
            Command:      []string{"/go-runner"},
            Args:         []string{fmt.Sprintf("-log-file=/var/log/kubeproxy-$(POD_NAME)%s.log", suffix), "-also-stdout=true", "/kubemark", "--morph=proxy", fmt.Sprintf("--name=$(POD_NAME)%s", suffix), "--kubeconfig=/kubeconfig/kubeconfig", "--v=4"},
            VolumeMounts: kubeconfigMounts,
            Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("25Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("100Mi")}},
        }

        containers = append(containers, kubeletContainer, proxyContainer)
    }

    ds := &appsv1.DaemonSet{
        ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace, Labels: labels},
        Spec: appsv1.DaemonSetSpec{
            Selector: &metav1.LabelSelector{MatchLabels: labels},
            Template: corev1.PodTemplateSpec{
                ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
                Spec: corev1.PodSpec{
                    ServiceAccountName: "hollow-node",
                    ImagePullSecrets: func() []corev1.LocalObjectReference { if secretName != "" { return []corev1.LocalObjectReference{{Name: secretName}} }; return nil }(),
                    Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.NodeSelectorOpDoesNotExist}, {Key: "kubemark", Operator: corev1.NodeSelectorOpDoesNotExist}, {Key: "node-role.kubernetes.io/worker", Operator: corev1.NodeSelectorOpExists}, {Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"k3s"}}}}}}}, PodAntiAffinity: &corev1.PodAntiAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{Weight: 100, PodAffinityTerm: corev1.PodAffinityTerm{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hollow-node"}}, TopologyKey: "kubernetes.io/hostname"}}}}},
                    Volumes: vols,
                    Containers: containers,
                    RestartPolicy: corev1.RestartPolicyAlways,
                },
            },
        },
    }

    return ds
}
