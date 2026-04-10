//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var Default = Build

func run(name string, args ...string) error {
	// Echo the command so mage shows progress
	fmt.Printf("• %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// Tidy runs go mod tidy
func Tidy() error {
	fmt.Println("==> Tidying modules")
	return run("go", "mod", "tidy")
}

// Build compiles the kube-inflater binary (unified inflater supporting all resource types including hollow nodes)
func Build() error {
	fmt.Println("==> Building kube-inflater 🎈")
	if err := run("go", "mod", "tidy"); err != nil {
		return err
	}
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := run("go", "build", "-o", filepath.Join(outDir, "kube-inflater"), "./cmd/kube-resource-inflater"); err != nil {
		return err
	}
	fmt.Println("==> Built bin/kube-inflater 🎈")
	fmt.Println("Usage examples:")
	fmt.Println("    ./bin/kube-inflater --resource-types=configmaps,secrets --count=1000")
	fmt.Println("    ./bin/kube-inflater --resource-types=hollownodes --count=100 --containers-per-pod=5")
	fmt.Println("    ./bin/kube-inflater --resource-types=hollownodes,configmaps --count=1000")
	fmt.Println("    ./bin/kube-inflater --cleanup-only --run-id=<id>")
	return nil
}

// Test runs `go test ./...`
func Test() error {
	fmt.Println("==> Running unit tests")
	return run("go", "test", "./...")
}

// Run executes the CLI with any extra args passed to mage after `--`.
// Example: mage run -- --resource-types=configmaps --count=100
func Run() error {
	fmt.Println("==> Running kube-inflater 🎈")
	bin := filepath.Join(".", "bin", "kube-inflater")
	if _, err := os.Stat(bin); err != nil {
		if err := Build(); err != nil {
			return err
		}
	}
	args := os.Args
	sep := 0
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	var pass []string
	if sep > 0 && sep+1 < len(args) {
		pass = args[sep+1:]
	}
	cmd := exec.Command(bin, pass...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// Clean removes build artifacts
func Clean() error {
	fmt.Println("==> Cleaning build artifacts")
	return os.RemoveAll("bin")
}

// BenchmarkUI builds the benchmark-ui server binary
func BenchmarkUI() error {
	fmt.Println("==> Building benchmark-ui 📊")
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := run("go", "build", "-o", filepath.Join(outDir, "benchmark-ui"), "./cmd/benchmark-ui"); err != nil {
		return err
	}
	fmt.Println("==> Built bin/benchmark-ui 📊")
	fmt.Println("Usage:")
	fmt.Println("    ./bin/benchmark-ui --reports-dir=./benchmark-reports --port=8080")
	return nil
}

// FrontendBuild runs npm build in the ui/ directory
func FrontendBuild() error {
	fmt.Println("==> Building frontend 🎨")
	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = "ui"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

const watchAgentImage = "k3sacr1.azurecr.io/watch-agent:latest"
const benchmarkUIImage = "k3sacr1.azurecr.io/benchmark-ui:latest"

// WatchAgent builds and pushes the watch-agent container image
func WatchAgent() error {
	fmt.Println("==> Building watch-agent image 📡")
	if err := run("docker", "build", "-f", "Dockerfile.watch-agent", "-t", watchAgentImage, "."); err != nil {
		return err
	}
	fmt.Println("==> Logging in to ACR")
	if err := run("az", "acr", "login", "--name", "k3sacr1"); err != nil {
		return fmt.Errorf("ACR login failed (ensure 'az' CLI is installed and logged in): %w", err)
	}
	fmt.Println("==> Pushing watch-agent image 📡")
	if err := run("docker", "push", watchAgentImage); err != nil {
		return err
	}
	fmt.Printf("==> Published %s\n", watchAgentImage)
	return nil
}

// BenchmarkUIImage builds and pushes the benchmark-ui container image
func BenchmarkUIImage() error {
	fmt.Println("==> Building benchmark-ui image 📊")
	if err := run("docker", "build", "--no-cache", "-f", "Dockerfile.benchmark-ui", "-t", benchmarkUIImage, "."); err != nil {
		return err
	}
	fmt.Println("==> Logging in to ACR")
	if err := run("az", "acr", "login", "--name", "k3sacr1"); err != nil {
		return fmt.Errorf("ACR login failed (ensure 'az' CLI is installed and logged in): %w", err)
	}
	fmt.Println("==> Pushing benchmark-ui image 📊")
	if err := run("docker", "push", benchmarkUIImage); err != nil {
		return err
	}
	fmt.Printf("==> Published %s\n", benchmarkUIImage)
	return nil
}

// UI builds the benchmark-ui image, pushes it, and deploys to the cluster.
func UI() error {
	if err := BenchmarkUIImage(); err != nil {
		return err
	}
	fmt.Println("==> Deploying benchmark-ui to cluster 🚀")
	if err := run("kubectl", "apply", "-f", "deploy/benchmark-ui/rbac.yaml"); err != nil {
		return err
	}
	if err := run("kubectl", "apply", "-f", "deploy/benchmark-ui/deployment.yaml"); err != nil {
		return err
	}
	if err := run("kubectl", "-n", "benchmark-ui", "rollout", "restart", "deployment/benchmark-ui"); err != nil {
		return err
	}
	// Wait for rollout; if the scheduler is backlogged the pod may stay Pending.
	// In that case, manually bind it to a control-plane node.
	if err := run("kubectl", "-n", "benchmark-ui", "rollout", "status", "deployment/benchmark-ui", "--timeout=60s"); err != nil {
		fmt.Println("==> Rollout timed out — attempting manual pod binding (scheduler may be backlogged)…")
		if podName, e := output("kubectl", "-n", "benchmark-ui", "get", "pods", "--field-selector=status.phase=Pending", "-o", "jsonpath={.items[0].metadata.name}"); e == nil && podName != "" {
			if node, e := output("kubectl", "get", "nodes", "-l", "node-role.kubernetes.io/control-plane=", "-o", "jsonpath={.items[0].metadata.name}"); e == nil && node != "" {
				fmt.Printf("==> Binding pod %s to node %s\n", podName, node)
				bindJSON := fmt.Sprintf(`{"apiVersion":"v1","kind":"Binding","metadata":{"name":"%s","namespace":"benchmark-ui"},"target":{"apiVersion":"v1","kind":"Node","name":"%s"}}`, podName, node)
				cmd := exec.Command("kubectl", "create", "-f", "-")
				cmd.Stdin = strings.NewReader(bindJSON)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				_ = cmd.Run()
			}
		}
		// Wait for the pod to become ready
		_ = run("kubectl", "-n", "benchmark-ui", "wait", "--for=condition=ready", "pod", "-l", "app=benchmark-ui", "--timeout=120s")
	}
	fmt.Println("==> benchmark-ui deployed ✅")
	// Print the access info
	_ = run("kubectl", "-n", "benchmark-ui", "get", "svc", "benchmark-ui")
	fmt.Println("==> Starting port-forward to localhost:6161 🔌")
	fmt.Println("    Open http://localhost:6161 in your browser")
	fmt.Println("    (auto-reconnects if the pod restarts, Ctrl+C to stop)")
	maxRetries := 30
	for i := 0; ; i++ {
		err := run("kubectl", "-n", "benchmark-ui", "port-forward", "svc/benchmark-ui", "6161:8080")
		if err == nil {
			return nil
		}
		if i >= maxRetries {
			return fmt.Errorf("port-forward failed after %d retries", maxRetries)
		}
		fmt.Println("==> port-forward lost, waiting for pod ready before reconnecting…")
		_ = run("kubectl", "-n", "benchmark-ui", "wait", "--for=condition=ready", "pod", "-l", "app=benchmark-ui", "--timeout=120s")
		fmt.Println("==> reconnecting port-forward…")
	}
}
