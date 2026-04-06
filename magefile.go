//go:build mage
// +build mage

// Performance Testing Usage:
// - mage perfReport              : Run with limit=1 (fast latency testing)
// - mage perfReportFull          : Run without limits (full data transfer testing)
// - mage perfReport -- --only-common : Run with limit=1 on common endpoints only
//
// The --no-limits flag can be dangerous with large clusters as it downloads
// all resources (e.g., 84MB for 20k nodes). Use perfReportFull for full testing.

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

// Tidy runs go mod tidy
func Tidy() error {
	fmt.Println("==> Tidying modules")
	return run("go", "mod", "tidy")
}

// Build compiles the binary to ./bin/kube-inflater
func Build() error {
	fmt.Println("==> Building kube-inflater 🎈")
	if err := run("go", "mod", "tidy"); err != nil {
		return err
	}
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := run("go", "build", "-o", filepath.Join(outDir, "kube-inflater"), "./cmd/kube-inflater"); err != nil {
		return err
	}
	fmt.Println("==> Built bin/kube-inflater 🎈")
	return nil
}

// Test runs `go test ./...`
func Test() error {
	fmt.Println("==> Running unit tests")
	return run("go", "test", "./...")
}

// Run executes the CLI with any extra args passed to mage after `--`.
// Example: mage run -- --nodes-to-add 10
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

// ResourceInflater builds the kube-resource-inflater binary
func ResourceInflater() error {
	fmt.Println("==> Building kube-resource-inflater 🏗️")
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := run("go", "build", "-o", filepath.Join(outDir, "kube-resource-inflater"), "./cmd/kube-resource-inflater"); err != nil {
		return err
	}
	fmt.Println("==> Built bin/kube-resource-inflater 🏗️")
	fmt.Println("Usage examples:")
	fmt.Println("    ./bin/kube-resource-inflater --resource-types=configmaps,secrets --count=1000")
	fmt.Println("    ./bin/kube-resource-inflater --resource-types=configmaps --count=100000 --workers=100 --qps=200")
	fmt.Println("    ./bin/kube-resource-inflater --cleanup-only")
	fmt.Println("    ./bin/kube-resource-inflater --cleanup-only --run-id=<id>")
	fmt.Println("    ./bin/kube-resource-inflater --resource-types=configmaps --count=100 --dry-run")
	return nil
}

// ResourceInflaterRun builds and runs kube-resource-inflater with args after --
func ResourceInflaterRun() error {
	fmt.Println("==> Running kube-resource-inflater 🏗️")
	bin := filepath.Join(".", "bin", "kube-resource-inflater")
	if _, err := os.Stat(bin); err != nil {
		if err := ResourceInflater(); err != nil {
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

// PerfReport builds and runs the API performance report generator
func PerfReport() error {
	fmt.Println("==> Building and running API performance report generator")

	// Build the performance report tool
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := run("go", "build", "-o", filepath.Join(outDir, "perf-report"), "./cmd/perf-report"); err != nil {
		return err
	}

	// Parse command line arguments after --
	args := os.Args
	sep := 0
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	var passArgs []string
	if sep > 0 && sep+1 < len(args) {
		passArgs = args[sep+1:]
	}

	// Run the performance report tool
	bin := filepath.Join(".", "bin", "perf-report")
	cmd := exec.Command(bin, passArgs...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// PerfReportFull builds and runs the API performance report generator without limits (full data)
// This mode returns complete resource lists instead of limited responses
func PerfReportFull() error {
	fmt.Println("==> Building and running API performance report generator (full data mode)")
	fmt.Println("⚠️  WARNING: This mode will download full resource lists and may take much longer")

	// Build the performance report tool
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := run("go", "build", "-o", filepath.Join(outDir, "perf-report"), "./cmd/perf-report"); err != nil {
		return err
	}

	// Run with no-limits flag
	bin := filepath.Join(".", "bin", "perf-report")
	cmd := exec.Command(bin, "--no-limits")
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
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

// AnalyzeNotReady builds and runs the NotReady pods analyzer
func AnalyzeNotReady() error {
	fmt.Println("==> Building and running NotReady pods analyzer")

	// Build the analyzer tool
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := run("go", "build", "-o", filepath.Join(outDir, "analyze-notready"), "./cmd/analyze-notready"); err != nil {
		return err
	}

	// Run the analyzer
	bin := filepath.Join(".", "bin", "analyze-notready")
	cmd := exec.Command(bin, "--details")
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// CleanupNodes builds the node cleanup utility
func CleanupNodes() error {
	fmt.Println("==> Building node cleanup utility")

	// Build the cleanup tool
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := run("go", "build", "-o", filepath.Join(outDir, "cleanup-nodes"), "./cmd/cleanup-nodes"); err != nil {
		return err
	}

	fmt.Println("✅ Built cleanup-nodes utility. Usage examples:")
	fmt.Println("    ./bin/cleanup-nodes --help")
	fmt.Println("    ./bin/cleanup-nodes --dry-run")
	fmt.Println("    ./bin/cleanup-nodes --zombie-only --force")
	fmt.Println("    ./bin/cleanup-nodes --notready-only --force")
	fmt.Println("    ./bin/cleanup-nodes --force  # Clean both zombie and NotReady nodes")
	fmt.Println("    ./bin/cleanup-nodes --include-pods --dry-run  # Also analyze pods")
	fmt.Println("    ./bin/cleanup-nodes --include-pods --pending-only --force")
	fmt.Println("    ./bin/cleanup-nodes --include-pods --failed-only --force")
	fmt.Println("    ./bin/cleanup-nodes --include-pods --force  # Clean nodes and problematic pods")
	return nil
}

// EtcdTester builds the etcd-tester binary (includes both testing and performance functionality)
func EtcdTester() error {
	fmt.Println("==> Building etcd-tester 🔧")

	// Build the etcd-tester tool from its module directory
	outDir := filepath.Join(".", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Change to etcd-tester directory and build
	cmd := exec.Command("go", "build", "-o", filepath.Join("..", "bin", "etcd-tester"))
	cmd.Dir = "etcd-tester"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println("✅ Built bin/etcd-tester - etcd connectivity and performance tester")
	fmt.Println("Usage examples:")
	fmt.Println("    ./bin/etcd-tester localhost:2379                    # Run connectivity tests")
	fmt.Println("    ./bin/etcd-tester perf localhost:2379 100 5        # Run performance test (100 nodes, 5 minutes)")
	fmt.Println("    ./bin/etcd-tester perf localhost:2379 1000 0       # Run performance test (1000 nodes, indefinite)")
	return nil
}
