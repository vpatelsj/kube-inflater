package podspec

import (
	cfgpkg "kube-inflater/internal/config"
	"testing"
)

func TestMakeHollowPodSpec_Basics(t *testing.T) {
	cfg := &cfgpkg.Config{Namespace: "ns", KubemarkImage: "img", NodeStatusFreq: "60s", NodeLeaseDuration: 120}
	pod := MakeHollowPodSpec(cfg, 3, 42)
	if pod.Namespace != "ns" {
		t.Fatalf("namespace mismatch")
	}
	if pod.Name != "hollow-node-42" {
		t.Fatalf("name mismatch")
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("expected 2 containers")
	}
}
