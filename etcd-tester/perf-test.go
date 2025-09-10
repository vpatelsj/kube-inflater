package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

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
	// Use shorter timeout for individual operations
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	lease, err := npt.client.Grant(opCtx, 300) // 300 second (5 minute) lease
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

	// Use shorter timeout for individual operations
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := npt.client.Put(opCtx, nodeKey, string(nodeJSON), clientv3.WithLease(node.LeaseID))
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

	// Use shorter timeout for individual operations
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Renew lease
	_, err := npt.client.KeepAliveOnce(opCtx, node.LeaseID)
	if err != nil {
		npt.metrics.IncrementCounter(&npt.metrics.Errors)
		// Don't return error to avoid stopping the lifecycle, just increment error counter
		return nil
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
	
	// Use shorter timeout for individual operations
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := npt.client.Put(opCtx, statusKey, string(statusJSON))
	if err != nil {
		npt.metrics.IncrementCounter(&npt.metrics.Errors)
		// Don't return error to avoid stopping the lifecycle, just increment error counter
		return nil
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
			npt.sendHeartbeat(ctx, node)
		case <-statusTicker.C:
			npt.updateNodeStatus(ctx, node)
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
			for range watchResp.Events {
				npt.metrics.IncrementCounter(&npt.metrics.WatchEvents)
			}
		}
	}
}

func (npt *NodePerfTest) printMetrics() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-npt.ctx.Done():
			fmt.Print("\n") // Move to next line when stopping
			return
		case <-ticker.C:
			created, deleted, heartbeats, statusUpdates, watchEvents, errors, totalOps, duration := npt.metrics.GetStats()
			opsPerSecond := float64(totalOps) / duration.Seconds()

			// Single line status update that overwrites itself
			fmt.Printf("\rNodes: %d | Created: %d | Deleted: %d | Heartbeats: %d | Status: %d | Watch: %d | Errors: %d | Ops/s: %.1f | Runtime: %.0fs",
				len(npt.nodes), created, deleted, heartbeats, statusUpdates, watchEvents, errors, opsPerSecond, duration.Seconds())
		}
	}
}

func (npt *NodePerfTest) Run() error {
	fmt.Printf("Starting performance test with %d nodes...\n", npt.nodeCount)

	// Start watch goroutine
	go npt.watchNodes(npt.ctx)

	// Start metrics printing
	go npt.printMetrics()

	fmt.Print("Creating nodes...")

	// Create initial nodes
	var wg sync.WaitGroup
	var nodesMutex sync.Mutex
	semaphore := make(chan struct{}, 10) // Limit concurrent node creation

	for i := 0; i < npt.nodeCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			node := npt.generateNodeInfo(index + 1)
			if err := npt.registerNode(npt.ctx, node); err != nil {
				return
			}

			// Thread-safe append to nodes slice
			nodesMutex.Lock()
			npt.nodes = append(npt.nodes, node)
			nodesMutex.Unlock()

			// Start lifecycle for this node
			go npt.nodeLifecycle(npt.ctx, node)
		}(i)
	}

	// Wait for all nodes to be created
	wg.Wait()
	fmt.Printf(" done! Created %d nodes.\n", len(npt.nodes))
	fmt.Println("Performance test running... Press Ctrl+C to stop.")

	// Start node churn simulation
	go npt.simulateNodeChurn(npt.ctx)

	// Keep running until interrupted
	<-npt.ctx.Done()
	return nil
}

func (npt *NodePerfTest) Stop() {
	fmt.Print("\nStopping performance test...")
	npt.cancel()

	// Clean up all nodes
	for _, node := range npt.nodes {
		nodeKey := fmt.Sprintf("/registry/nodes/%s", node.Name)
		npt.client.Delete(context.Background(), nodeKey)
	}
	fmt.Println(" cleanup completed")
}

// RunNodePerfTest runs a performance test against etcd with the specified configuration
func RunNodePerfTest(endpoint string, nodeCount int, duration time.Duration) error {
	fmt.Printf("Connecting to etcd at %s...\n", endpoint)
	
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 30 * time.Second, // Increased timeout for remote servers
	})
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %v", err)
	}
	defer client.Close()

	// Test the connection before starting performance test
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	fmt.Print("Testing connection...")
	_, err = client.Get(ctx, "/test-connection")
	if err != nil {
		fmt.Printf(" failed!\n")
		return fmt.Errorf("failed to test etcd connection: %v", err)
	}
	fmt.Printf(" connected successfully!\n")

	perfTest := NewNodePerfTest(client, nodeCount)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle graceful shutdown on signal
	go func() {
		<-sigChan
		fmt.Print("\nReceived interrupt signal...")
		perfTest.Stop()
	}()

	// Optional: Handle duration-based shutdown if duration > 0
	if duration > 0 {
		go func() {
			time.Sleep(duration)
			fmt.Printf("\nDuration %v completed...", duration)
			perfTest.Stop()
		}()
	}

	return perfTest.Run()
}


