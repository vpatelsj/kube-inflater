package config

import "time"

// Defaults for CLI values
const (
	DefaultWaitTimeoutSec       = 3000
	DefaultPerfWaitSec          = 30
	DefaultPerfTests            = 5
	DefaultKubemarkImage        = "k3sacr1.azurecr.io/kubemark:node-heartbeat-optimized-latest"
	DefaultNamespace            = "kubemark-incremental-test"
	DefaultContainersPerPod     = 5
)

// DefaultTokenAudiences lists common API server audiences for SA tokens.
var DefaultTokenAudiences = []string{"https://kubernetes.default.svc", "kubernetes", "k3s", "kube-apiserver", "api"}

type Config struct {
	StrictTarget      bool
	AutoExpected      bool
	WaitTimeout       time.Duration
	PerfWait          time.Duration
	PerfTests         int
	KubemarkImage     string
	NodeStatusFreq    string
	NodeLeaseDuration int
	NodeMonitorGrace  string
	Namespace         string
	ContainersPerPod  int
	// TokenAudiences configures the audiences used when requesting a ServiceAccount token
	// for the hollow-node kubeconfig. If empty, DefaultTokenAudiences is used.
	TokenAudiences []string
	// SkipPerfTests disables performance measurements entirely
	SkipPerfTests bool
	// CleanupOnly triggers resource cleanup and exit
	CleanupOnly bool
}
