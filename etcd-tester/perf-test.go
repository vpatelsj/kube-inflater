package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type NodePerfTest struct {
	client    *clientv3.Client
	nodeCount int
	nodes     []*NodeInfo
	metrics   *PerfMetrics
	ctx       context.Context
	cancel    context.CancelFunc
}

type NodeInfo struct {
	Name     string
	ID       string
	IP       string
	Status   string
	LeaseID  clientv3.LeaseID
	LastSeen time.Time
	mutex    sync.RWMutex
}

type PerfMetrics struct {
	NodesCreated    int64
	NodesDeleted    int64
	HeartbeatsSent  int64
	StatusUpdates   int64
	WatchEvents     int64
	Errors          int64
	TotalOperations int64
	StartTime       time.Time
	mutex           sync.RWMutex
}

func NewNodePerfTest(client *clientv3.Client, nodeCount int) *NodePerfTest {
	ctx, cancel := context.WithCancel(context.Background())
	return &NodePerfTest{
		client:    client,
		nodeCount: nodeCount,
		nodes:     make([]*NodeInfo, 0, nodeCount),
		metrics:   &PerfMetrics{StartTime: time.Now()},
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (p *PerfMetrics) IncrementCounter(counter *int64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	*counter++
	p.TotalOperations++
}

func (p *PerfMetrics) GetStats() (int64, int64, int64, int64, int64, int64, int64, time.Duration) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	duration := time.Since(p.StartTime)
	return p.NodesCreated, p.NodesDeleted, p.HeartbeatsSent, p.StatusUpdates,
		p.WatchEvents, p.Errors, p.TotalOperations, duration
}

func (npt *NodePerfTest) generateNodeInfo(index int) *NodeInfo {
	return &NodeInfo{
		Name:     fmt.Sprintf("perf-node-%03d", index),
		ID:       fmt.Sprintf("perf-node-%03d-id-%d", index, time.Now().Unix()),
		IP:       fmt.Sprintf("192.168.%d.%d", (index/254)+1, (index%254)+1),
		Status:   "Ready",
		LastSeen: time.Now(),
	}
}

func (npt *NodePerfTest) createNodeLease(ctx context.Context, node *NodeInfo) error {
	lease, err := npt.client.Grant(ctx, 60) // 60 second lease
	if err != nil {
		npt.metrics.IncrementCounter(&npt.metrics.Errors)
		return fmt.Errorf("failed to create lease for node %s: %v", node.Name, err)
	}
	node.LeaseID = lease.ID
	return nil
}

func (npt *NodePerfTest) registerNode(ctx context.Context, node *NodeInfo) error {
	if err := npt.createNodeLease(ctx, node); err != nil {
		return err
	}

	nodeData := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": node.Name,
			"uid":  node.ID,
			"labels": map[string]string{
				"kubernetes.io/arch":             "amd64",
				"kubernetes.io/os":               "linux",
				"kubernetes.io/hostname":         node.Name,
				"node-role.kubernetes.io/worker": "",
			},
			"annotations": map[string]string{
				"node.alpha.kubernetes.io/ttl":                           "0",
				"volumes.kubernetes.io/controller-managed-attach-detach": "true",
			},
		},
		"spec": map[string]interface{}{
			"podCIDR": fmt.Sprintf("10.%d.0.0/24", rand.Intn(255)),
		},
		"status": map[string]interface{}{
			"addresses": []map[string]string{
				{"type": "InternalIP", "address": node.IP},
				{"type": "Hostname", "address": node.Name},
			},
			"conditions": []map[string]interface{}{
				{
					"type":               "Ready",
					"status":             "True",
					"lastHeartbeatTime":  time.Now().Format(time.RFC3339),
					"lastTransitionTime": time.Now().Format(time.RFC3339),
					"reason":             "KubeletReady",
					"message":            "kubelet is posting ready status",
				},
			},
			"nodeInfo": map[string]string{
				"architecture":            "amd64",
				"bootID":                  fmt.Sprintf("boot-%s", node.ID[:8]),
				"containerRuntimeVersion": "containerd://1.6.24",
				"kernelVersion":           "5.15.0-86-generic",
				"kubeProxyVersion":        "v1.28.2",
				"kubeletVersion":          "v1.28.2",
				"machineID":               fmt.Sprintf("machine-%s", node.ID[:16]),
				"operatingSystem":         "linux",
				"osImage":                 "Ubuntu 20.04.6 LTS",
				"systemUUID":              fmt.Sprintf("system-%s", node.ID),
			},
			"capacity": map[string]string{
				"cpu":               "4",
				"ephemeral-storage": "100Gi",
				"hugepages-1Gi":     "0",
				"hugepages-2Mi":     "0",
				"memory":            "8Gi",
				"pods":              "110",
			},
			"allocatable": map[string]string{
				"cpu":               "3900m",
				"ephemeral-storage": "92233720368547758592",
				"hugepages-1Gi":     "0",
				"hugepages-2Mi":     "0",
				"memory":            "7538Mi",
				"pods":              "110",
			},
		},
	}

	nodeKey := fmt.Sprintf("/registry/nodes/%s", node.Name)
	nodeJSON, _ := json.Marshal(nodeData)

	_, err := npt.client.Put(ctx, nodeKey, string(nodeJSON), clientv3.WithLease(node.LeaseID))
	if err != nil {
		npt.metrics.IncrementCounter(&npt.metrics.Errors)
		return fmt.Errorf("failed to register node %s: %v", node.Name, err)
	}

	npt.metrics.IncrementCounter(&npt.metrics.NodesCreated)
	return nil
}

func (npt *NodePerfTest) sendHeartbeat(ctx context.Context, node *NodeInfo) error {
	node.mutex.Lock()
	node.LastSeen = time.Now()
	node.mutex.Unlock()

	// Renew lease
	_, err := npt.client.KeepAliveOnce(ctx, node.LeaseID)
	if err != nil {
		npt.metrics.IncrementCounter(&npt.metrics.Errors)
		return fmt.Errorf("failed to send heartbeat for node %s: %v", node.Name, err)
	}

	npt.metrics.IncrementCounter(&npt.metrics.HeartbeatsSent)
	return nil
}

func (npt *NodePerfTest) updateNodeStatus(ctx context.Context, node *NodeInfo) error {
	// Simulate random status changes
	statuses := []string{"Ready", "NotReady", "SchedulingDisabled"}
	newStatus := statuses[rand.Intn(len(statuses))]

	node.mutex.Lock()
	node.Status = newStatus
	node.mutex.Unlock()

	statusKey := fmt.Sprintf("/registry/nodes/%s/status", node.Name)
	statusData := map[string]interface{}{
		"conditions": []map[string]interface{}{
			{
				"type":               "Ready",
				"status":             fmt.Sprintf("%v", newStatus == "Ready"),
				"lastHeartbeatTime":  time.Now().Format(time.RFC3339),
				"lastTransitionTime": time.Now().Format(time.RFC3339),
				"reason":             "KubeletStatusUpdate",
				"message":            fmt.Sprintf("Node status updated to %s", newStatus),
			},
		},
	}

	statusJSON, _ := json.Marshal(statusData)
	_, err := npt.client.Put(ctx, statusKey, string(statusJSON))
	if err != nil {
		npt.metrics.IncrementCounter(&npt.metrics.Errors)
		return fmt.Errorf("failed to update status for node %s: %v", node.Name, err)
	}

	npt.metrics.IncrementCounter(&npt.metrics.StatusUpdates)
	return nil
}

func (npt *NodePerfTest) simulateNodeChurn(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Randomly delete and recreate 5% of nodes to simulate churn
			churnCount := npt.nodeCount / 20
			if churnCount < 1 {
				churnCount = 1
			}

			for i := 0; i < churnCount; i++ {
				if len(npt.nodes) == 0 {
					continue
				}

				// Select random node to delete
				nodeIndex := rand.Intn(len(npt.nodes))
				node := npt.nodes[nodeIndex]

				// Delete node
				nodeKey := fmt.Sprintf("/registry/nodes/%s", node.Name)
				_, err := npt.client.Delete(ctx, nodeKey)
				if err == nil {
					npt.metrics.IncrementCounter(&npt.metrics.NodesDeleted)
				} else {
					npt.metrics.IncrementCounter(&npt.metrics.Errors)
				}

				// Remove from slice
				npt.nodes = append(npt.nodes[:nodeIndex], npt.nodes[nodeIndex+1:]...)

				// Create new node
				newNode := npt.generateNodeInfo(len(npt.nodes) + i + 1000)
				if err := npt.registerNode(ctx, newNode); err == nil {
					npt.nodes = append(npt.nodes, newNode)
				}
			}
		}
	}
}

func (npt *NodePerfTest) nodeLifecycle(ctx context.Context, node *NodeInfo) {
	heartbeatTicker := time.NewTicker(10 * time.Second)
	statusTicker := time.NewTicker(60 * time.Second)
	defer heartbeatTicker.Stop()
	defer statusTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			if err := npt.sendHeartbeat(ctx, node); err != nil {
				log.Printf("Heartbeat error for %s: %v", node.Name, err)
			}
		case <-statusTicker.C:
			if err := npt.updateNodeStatus(ctx, node); err != nil {
				log.Printf("Status update error for %s: %v", node.Name, err)
			}
		}
	}
}

func (npt *NodePerfTest) watchNodes(ctx context.Context) {
	watchChan := npt.client.Watch(ctx, "/registry/nodes/", clientv3.WithPrefix())

	for {
		select {
		case <-ctx.Done():
			return
		case watchResp := <-watchChan:
			for _, event := range watchResp.Events {
				npt.metrics.IncrementCounter(&npt.metrics.WatchEvents)
				if event.Type == mvccpb.PUT {
					log.Printf("Watch: Node %s updated", string(event.Kv.Key))
				} else if event.Type == mvccpb.DELETE {
					log.Printf("Watch: Node %s deleted", string(event.Kv.Key))
				}
			}
		}
	}
}

func (npt *NodePerfTest) printMetrics() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-npt.ctx.Done():
			return
		case <-ticker.C:
			created, deleted, heartbeats, statusUpdates, watchEvents, errors, totalOps, duration := npt.metrics.GetStats()
			opsPerSecond := float64(totalOps) / duration.Seconds()

			fmt.Printf("\n=== Performance Metrics (%.1fs) ===\n", duration.Seconds())
			fmt.Printf("Active Nodes: %d\n", len(npt.nodes))
			fmt.Printf("Nodes Created: %d\n", created)
			fmt.Printf("Nodes Deleted: %d\n", deleted)
			fmt.Printf("Heartbeats Sent: %d\n", heartbeats)
			fmt.Printf("Status Updates: %d\n", statusUpdates)
			fmt.Printf("Watch Events: %d\n", watchEvents)
			fmt.Printf("Errors: %d\n", errors)
			fmt.Printf("Total Operations: %d\n", totalOps)
			fmt.Printf("Operations/Second: %.2f\n", opsPerSecond)
			fmt.Printf("=======================================\n")
		}
	}
}

func (npt *NodePerfTest) Run() error {
	fmt.Printf("Starting performance test with %d nodes...\n", npt.nodeCount)

	// Start watch goroutine
	go npt.watchNodes(npt.ctx)

	// Start metrics printing
	go npt.printMetrics()

	// Create initial nodes
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent node creation

	for i := 0; i < npt.nodeCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			node := npt.generateNodeInfo(index + 1)
			if err := npt.registerNode(npt.ctx, node); err != nil {
				log.Printf("Failed to register node %s: %v", node.Name, err)
				return
			}

			npt.nodes = append(npt.nodes, node)

			// Start lifecycle for this node
			go npt.nodeLifecycle(npt.ctx, node)
		}(i)
	}

	// Wait for all nodes to be created
	wg.Wait()
	fmt.Printf("Successfully created %d nodes\n", len(npt.nodes))

	// Start node churn simulation
	go npt.simulateNodeChurn(npt.ctx)

	// Keep running until interrupted
	<-npt.ctx.Done()
	return nil
}

func (npt *NodePerfTest) Stop() {
	fmt.Println("Stopping performance test...")
	npt.cancel()

	// Clean up all nodes
	for _, node := range npt.nodes {
		nodeKey := fmt.Sprintf("/registry/nodes/%s", node.Name)
		npt.client.Delete(context.Background(), nodeKey)
	}
	fmt.Println("Cleanup completed")
}

func runPerfTest(endpoint string, nodeCount int) error {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %v", err)
	}
	defer client.Close()

	perfTest := NewNodePerfTest(client, nodeCount)

	// Handle graceful shutdown
	go func() {
		time.Sleep(300 * time.Second) // Run for 5 minutes by default
		perfTest.Stop()
	}()

	return perfTest.Run()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run perf-test.go <etcd-endpoint> [node-count]")
		fmt.Println("Example: go run perf-test.go localhost:2379 100")
		os.Exit(1)
	}

	endpoint := os.Args[1]
	nodeCount := 100

	if len(os.Args) >= 3 {
		if count, err := strconv.Atoi(os.Args[2]); err == nil {
			nodeCount = count
		}
	}

	fmt.Printf("Starting etcd performance test with %d nodes against %s\n", nodeCount, endpoint)

	if err := runPerfTest(endpoint, nodeCount); err != nil {
		log.Fatalf("Performance test failed: %v", err)
	}
}
