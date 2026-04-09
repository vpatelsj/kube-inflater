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
