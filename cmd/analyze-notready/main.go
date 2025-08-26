package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type NodeAnalysis struct {
	NodeName        string
	NotReadyCount   int
	TotalPods       int
	NotReadyPods    []string
	PhysicalNode    string
	PhysicalStatus  string
	NonRunningCount int
	NonRunningPods  []PodStatus
	RunningCount    int
	HasHollowPods   bool     // Track if this physical node has hollow pods
	ZombieNodes     []string // Track hollow nodes without corresponding pods
	ZombieNodeCount int      // Count of zombie nodes
}

type PodStatus struct {
	Name   string
	Status string
	Reason string
}

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}

	namespace := flag.String("namespace", "kubemark-incremental-test", "namespace to analyze")
	showDetails := flag.Bool("details", false, "show detailed pod information")
	showPodStatus := flag.Bool("pod-status", false, "show non-running pod status details")
	sortBy := flag.String("sort", "notready", "sort by: notready, total, node, nonrunning, or zombie")

	flag.Parse()

	// Build kubernetes client
	var config *rest.Config
	var err error

	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building kubernetes config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating kubernetes client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	fmt.Printf("🔍 Analyzing NotReady pods in namespace: %s\n\n", *namespace)

	// Get all pods in the namespace
	pods, err := clientset.CoreV1().Pods(*namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing pods: %v\n", err)
		os.Exit(1)
	}

	// Get all nodes to check their status
	allNodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing nodes: %v\n", err)
		os.Exit(1)
	}

	// Create a map of node statuses
	nodeStatus := make(map[string]string)
	// Track which hollow nodes have corresponding pods
	hollowNodesWithPods := make(map[string]bool)
	physicalNodes := make(map[string]bool)

	for _, node := range allNodes.Items {
		status := "Ready"
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status != "True" {
				status = "NotReady"
				break
			}
		}
		nodeStatus[node.Name] = status

		// Track physical nodes (nodes without hollow-node prefix)
		if !strings.HasPrefix(node.Name, "hollow-node") {
			physicalNodes[node.Name] = true
		}
	}

	// Analyze pods by physical node
	nodeAnalysis := make(map[string]*NodeAnalysis)

	for _, pod := range pods.Items {
		physicalNode := pod.Spec.NodeName
		if physicalNode == "" {
			continue // Skip unscheduled pods
		}

		// Track that this hollow node has a corresponding pod
		hollowNodesWithPods[pod.Name] = true

		// Initialize node analysis if not exists
		if _, exists := nodeAnalysis[physicalNode]; !exists {
			nodeAnalysis[physicalNode] = &NodeAnalysis{
				NodeName:       physicalNode,
				PhysicalNode:   physicalNode,
				PhysicalStatus: nodeStatus[physicalNode],
				NotReadyPods:   []string{},
				NonRunningPods: []PodStatus{},
				ZombieNodes:    []string{},
				HasHollowPods:  true,
			}
		}

		analysis := nodeAnalysis[physicalNode]
		analysis.TotalPods++

		// Check pod status
		podPhase := string(pod.Status.Phase)
		isRunning := podPhase == "Running"

		if !isRunning {
			analysis.NonRunningCount++
			reason := ""
			if len(pod.Status.ContainerStatuses) > 0 {
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.State.Waiting != nil {
						reason = cs.State.Waiting.Reason
						break
					}
					if cs.State.Terminated != nil {
						reason = cs.State.Terminated.Reason
						break
					}
				}
			}
			analysis.NonRunningPods = append(analysis.NonRunningPods, PodStatus{
				Name:   pod.Name,
				Status: podPhase,
				Reason: reason,
			})
		} else {
			analysis.RunningCount++
		}

		// Check if the hollow node (not the pod) is NotReady
		hollowNodeName := pod.Name
		if hollowNodeStatus, exists := nodeStatus[hollowNodeName]; exists && hollowNodeStatus == "NotReady" {
			analysis.NotReadyCount++
			analysis.NotReadyPods = append(analysis.NotReadyPods, hollowNodeName)
		}
	}

	// Find zombie hollow nodes (nodes that exist but have no corresponding pods)
	for nodeName := range nodeStatus {
		if strings.HasPrefix(nodeName, "hollow-node") {
			if !hollowNodesWithPods[nodeName] {
				// This is a zombie node - it exists in cluster but has no pod
				// Try to determine which physical node it was originally scheduled to
				// Since we can't determine this from a missing pod, we'll create a special entry

				zombiePhysicalNode := "unknown-physical-node"

				// Try to find the physical node from existing analysis or create a new entry
				var targetAnalysis *NodeAnalysis

				// For now, let's create a special entry for zombie nodes
				if _, exists := nodeAnalysis[zombiePhysicalNode]; !exists {
					nodeAnalysis[zombiePhysicalNode] = &NodeAnalysis{
						NodeName:       zombiePhysicalNode,
						PhysicalNode:   zombiePhysicalNode,
						PhysicalStatus: "Unknown",
						NotReadyPods:   []string{},
						NonRunningPods: []PodStatus{},
						ZombieNodes:    []string{},
						HasHollowPods:  false,
					}
				}

				targetAnalysis = nodeAnalysis[zombiePhysicalNode]
				targetAnalysis.ZombieNodeCount++
				targetAnalysis.ZombieNodes = append(targetAnalysis.ZombieNodes, nodeName)
			}
		}
	}

	// Convert to slice for sorting
	var results []*NodeAnalysis
	for _, analysis := range nodeAnalysis {
		results = append(results, analysis)
	}

	// Sort results
	sort.Slice(results, func(i, j int) bool {
		switch *sortBy {
		case "total":
			if results[i].TotalPods != results[j].TotalPods {
				return results[i].TotalPods > results[j].TotalPods
			}
		case "node":
			return results[i].NodeName < results[j].NodeName
		case "nonrunning":
			if results[i].NonRunningCount != results[j].NonRunningCount {
				return results[i].NonRunningCount > results[j].NonRunningCount
			}
		case "zombie":
			if results[i].ZombieNodeCount != results[j].ZombieNodeCount {
				return results[i].ZombieNodeCount > results[j].ZombieNodeCount
			}
		default: // "notready"
			if results[i].NotReadyCount != results[j].NotReadyCount {
				return results[i].NotReadyCount > results[j].NotReadyCount
			}
		}
		return results[i].NodeName < results[j].NodeName
	})

	// Display results
	fmt.Printf("📊 Summary by Physical Node:\n")
	fmt.Printf("%-20s %-10s %-10s %-10s %-10s %-10s %-10s %-10s\n", "Physical Node", "Status", "NotReady", "NonRun", "Running", "Zombie", "Total", "H.Node%")
	fmt.Printf("%-20s %-10s %-10s %-10s %-10s %-10s %-10s %-10s\n",
		strings.Repeat("-", 20), strings.Repeat("-", 10), strings.Repeat("-", 10),
		strings.Repeat("-", 10), strings.Repeat("-", 10), strings.Repeat("-", 10),
		strings.Repeat("-", 10), strings.Repeat("-", 10))

	totalNotReady := 0
	totalPods := 0
	totalNonRunning := 0
	totalRunning := 0
	totalZombieNodes := 0
	totalNotReadyNodes := 0 // Total count of all NotReady nodes (with and without pods)

	for _, analysis := range results {
		rate := "0%"
		if analysis.TotalPods > 0 {
			rate = fmt.Sprintf("%.1f%%", float64(analysis.NotReadyCount)/float64(analysis.TotalPods)*100)
		}

		statusIcon := "✅"
		if analysis.PhysicalStatus == "NotReady" {
			statusIcon = "❌"
		}

		fmt.Printf("%-20s %-10s %-10d %-10d %-10d %-10d %-10d %-10s\n",
			analysis.NodeName,
			statusIcon+" "+analysis.PhysicalStatus,
			analysis.NotReadyCount,
			analysis.NonRunningCount,
			analysis.RunningCount,
			analysis.ZombieNodeCount,
			analysis.TotalPods,
			rate)

		totalNotReady += analysis.NotReadyCount
		totalPods += analysis.TotalPods
		totalNonRunning += analysis.NonRunningCount
		totalRunning += analysis.RunningCount
		totalZombieNodes += analysis.ZombieNodeCount

		// Show details if requested
		if *showDetails && analysis.NotReadyCount > 0 {
			fmt.Printf("  └─ NotReady hollow nodes: ")
			if len(analysis.NotReadyPods) <= 5 {
				fmt.Printf("%s\n", strings.Join(analysis.NotReadyPods, ", "))
			} else {
				fmt.Printf("%s, ... (+%d more)\n",
					strings.Join(analysis.NotReadyPods[:5], ", "),
					len(analysis.NotReadyPods)-5)
			}
		}

		// Show pod status details if requested
		if *showPodStatus && analysis.NonRunningCount > 0 {
			fmt.Printf("  └─ Non-running pods: ")
			if len(analysis.NonRunningPods) <= 5 {
				for i, pod := range analysis.NonRunningPods[:min(5, len(analysis.NonRunningPods))] {
					if i > 0 {
						fmt.Printf(", ")
					}
					fmt.Printf("%s(%s)", pod.Name, pod.Status)
					if pod.Reason != "" {
						fmt.Printf(":%s", pod.Reason)
					}
				}
				if len(analysis.NonRunningPods) > 5 {
					fmt.Printf(", ... (+%d more)", len(analysis.NonRunningPods)-5)
				}
				fmt.Printf("\n")
			}
		}

		// Show zombie node details if present
		if analysis.ZombieNodeCount > 0 {
			fmt.Printf("  └─ Zombie hollow nodes (no pods): ")
			if len(analysis.ZombieNodes) <= 5 {
				fmt.Printf("%s\n", strings.Join(analysis.ZombieNodes, ", "))
			} else {
				fmt.Printf("%s, ... (+%d more)\n",
					strings.Join(analysis.ZombieNodes[:5], ", "),
					len(analysis.ZombieNodes)-5)
			}
		}
	}

	// Calculate total NotReady nodes (including both those with pods and zombie nodes)
	for nodeName, status := range nodeStatus {
		if strings.HasPrefix(nodeName, "hollow-node") && status == "NotReady" {
			totalNotReadyNodes++
		}
	}

	fmt.Printf("\n📈 Overall Statistics:\n")
	fmt.Printf("Total NotReady hollow nodes (all): %d\n", totalNotReadyNodes)
	fmt.Printf("├─ NotReady nodes with pods: %d\n", totalNotReady)
	fmt.Printf("└─ NotReady zombie nodes: %d\n", totalNotReadyNodes-totalNotReady)
	fmt.Printf("Total zombie hollow nodes: %d\n", totalZombieNodes)
	fmt.Printf("Total non-running pods: %d\n", totalNonRunning)
	fmt.Printf("Total running pods: %d\n", totalRunning)
	fmt.Printf("Total hollow node pods: %d\n", totalPods)
	if totalPods > 0 {
		fmt.Printf("Overall NotReady rate: %.2f%%\n", float64(totalNotReady)/float64(totalPods)*100)
		fmt.Printf("Overall non-running rate: %.2f%%\n", float64(totalNonRunning)/float64(totalPods)*100)
	}
	if totalNotReadyNodes > 0 {
		totalRegisteredHollowNodes := totalPods + totalZombieNodes
		if totalRegisteredHollowNodes > 0 {
			fmt.Printf("Overall hollow node NotReady rate: %.2f%% (%d out of %d registered hollow nodes)\n",
				float64(totalNotReadyNodes)/float64(totalRegisteredHollowNodes)*100,
				totalNotReadyNodes, totalRegisteredHollowNodes)
		}
	}
	if totalZombieNodes > 0 {
		fmt.Printf("⚠️  Found %d zombie hollow nodes (nodes without corresponding pods)\n", totalZombieNodes)
	}

	// Show physical node health summary
	healthyNodes := 0
	unhealthyNodes := 0
	for _, analysis := range results {
		if analysis.PhysicalStatus == "Ready" {
			healthyNodes++
		} else {
			unhealthyNodes++
		}
	}

	fmt.Printf("\n🏥 Physical Node Health:\n")
	fmt.Printf("Healthy physical nodes: %d\n", healthyNodes)
	fmt.Printf("Unhealthy physical nodes: %d\n", unhealthyNodes)

	if unhealthyNodes > 0 {
		fmt.Printf("\n⚠️  Issues detected:\n")
		for _, analysis := range results {
			if analysis.PhysicalStatus == "NotReady" {
				fmt.Printf("- Physical node %s is NotReady (%d hollow nodes affected)\n",
					analysis.NodeName, analysis.NotReadyCount)
			}
		}
	}
}
