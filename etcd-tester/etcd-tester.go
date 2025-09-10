package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
)

// TestConfig holds configuration for etcd testing
type TestConfig struct {
	Endpoints   []string
	CAFile      string
	CertFile    string
	KeyFile     string
	Username    string
	Password    string
	DialTimeout time.Duration
}

// EtcdTester tests etcd endpoints and operations used by Kubernetes
type EtcdTester struct {
	client *clientv3.Client
	config *TestConfig
}

// TestResult represents the result of a test
type TestResult struct {
	Name     string        `json:"name"`
	Success  bool          `json:"success"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
	Details  interface{}   `json:"details,omitempty"`
}

// EtcdHealthResponse represents etcd health check response
type EtcdHealthResponse struct {
	Health string `json:"health"`
}

// NewEtcdTester creates a new etcd tester
func NewEtcdTester(config *TestConfig) (*EtcdTester, error) {
	fmt.Println("    🔧 DEBUG: Configuring etcd client...")
	var tlsConfig *tls.Config
	var err error

	// Setup TLS if certificates are provided
	if config.CAFile != "" || config.CertFile != "" || config.KeyFile != "" {
		fmt.Println("    🔧 DEBUG: Setting up TLS configuration...")
		tlsInfo := transport.TLSInfo{
			CertFile:      config.CertFile,
			KeyFile:       config.KeyFile,
			TrustedCAFile: config.CAFile,
		}
		tlsConfig, err = tlsInfo.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS config: %v", err)
		}
	}

	fmt.Printf("    🔧 DEBUG: Creating client config with endpoints: %v, timeout: %v\n", config.Endpoints, config.DialTimeout)
	clientConfig := clientv3.Config{
		Endpoints:   config.Endpoints,
		DialTimeout: config.DialTimeout,
		DialOptions: []grpc.DialOption{
			grpc.WithBlock(),
		},
		TLS: tlsConfig,
	}

	// Setup authentication if provided
	if config.Username != "" && config.Password != "" {
		fmt.Printf("    🔧 DEBUG: Setting up authentication for user: %s\n", config.Username)
		clientConfig.Username = config.Username
		clientConfig.Password = config.Password
	}

	fmt.Println("    🔧 DEBUG: Connecting to etcd...")
	client, err := clientv3.New(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %v", err)
	}
	fmt.Println("    🔧 DEBUG: Etcd client created successfully!")

	return &EtcdTester{
		client: client,
		config: config,
	}, nil
}

// Close closes the etcd client
func (t *EtcdTester) Close() error {
	if t.client != nil {
		return t.client.Close()
	}
	return nil
}

// RunAllTests runs all etcd tests
func (t *EtcdTester) RunAllTests(ctx context.Context) []TestResult {
	var results []TestResult

	fmt.Println("🔧 DEBUG: Starting all etcd tests...")

	// Connection and cluster tests
	fmt.Println("🔧 DEBUG: Running connection test...")
	results = append(results, t.testConnection(ctx))
	fmt.Printf("🔧 DEBUG: Connection test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running member list test...")
	results = append(results, t.testMemberList(ctx))
	fmt.Printf("🔧 DEBUG: Member list test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running cluster health test...")
	results = append(results, t.testClusterHealth(ctx))
	fmt.Printf("🔧 DEBUG: Cluster health test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running endpoint status test...")
	results = append(results, t.testEndpointStatus(ctx))
	fmt.Printf("🔧 DEBUG: Endpoint status test completed: %v\n", results[len(results)-1].Success)

	// Basic KV operations (used by Kubernetes storage)
	fmt.Println("🔧 DEBUG: Running PUT test...")
	results = append(results, t.testPut(ctx))
	fmt.Printf("🔧 DEBUG: PUT test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running PUT Random Keys test...")
	results = append(results, t.testPutRandomKeys(ctx))
	fmt.Printf("🔧 DEBUG: PUT Random Keys test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running GET test...")
	results = append(results, t.testGet(ctx))
	fmt.Printf("🔧 DEBUG: GET test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running DELETE test...")
	results = append(results, t.testDelete(ctx))
	fmt.Printf("🔧 DEBUG: DELETE test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running transaction test...")
	results = append(results, t.testTxn(ctx))
	fmt.Printf("🔧 DEBUG: Transaction test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running compact test...")
	results = append(results, t.testCompact(ctx))
	fmt.Printf("🔧 DEBUG: Compact test completed: %v\n", results[len(results)-1].Success)

	// Range operations (used for LIST operations)
	fmt.Println("🔧 DEBUG: Running range test...")
	results = append(results, t.testRange(ctx))
	fmt.Printf("🔧 DEBUG: Range test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running range with limit test...")
	results = append(results, t.testRangeWithLimit(ctx))
	fmt.Printf("🔧 DEBUG: Range with limit test completed: %v\n", results[len(results)-1].Success)

	// Watch operations (used for watch API)
	fmt.Println("🔧 DEBUG: Running watch test...")
	results = append(results, t.testWatch(ctx))
	fmt.Printf("🔧 DEBUG: Watch test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running watch from revision test...")
	results = append(results, t.testWatchFromRevision(ctx))
	fmt.Printf("🔧 DEBUG: Watch from revision test completed: %v\n", results[len(results)-1].Success)

	// Lease operations (used for TTL) - This is likely where it hangs!
	fmt.Println("🔧 DEBUG: Running LEASE test (this may hang)...")
	results = append(results, t.testLease(ctx))
	fmt.Printf("🔧 DEBUG: LEASE test completed: %v\n", results[len(results)-1].Success)

	// Auth operations (if auth is enabled)
	if t.config.Username != "" {
		fmt.Println("🔧 DEBUG: Running auth test...")
		results = append(results, t.testAuth(ctx))
		fmt.Printf("🔧 DEBUG: Auth test completed: %v\n", results[len(results)-1].Success)
	}

	// Performance tests
	fmt.Println("🔧 DEBUG: Running performance test...")
	results = append(results, t.testPerformance(ctx))
	fmt.Printf("🔧 DEBUG: Performance test completed: %v\n", results[len(results)-1].Success)
	
	fmt.Println("🔧 DEBUG: Running basic performance test...")
	results = append(results, t.testPerformanceBasic())
	fmt.Printf("🔧 DEBUG: Basic performance test completed: %v\n", results[len(results)-1].Success)

	// Kubernetes-specific tests
	fmt.Println("🔧 DEBUG: Running Kubernetes patterns test...")
	results = append(results, t.testKubernetesPatterns(ctx))
	fmt.Printf("🔧 DEBUG: Kubernetes patterns test completed: %v\n", results[len(results)-1].Success)

	// Node-related tests (Kubernetes node operations)
	fmt.Println("🔧 DEBUG: Running node operations test...")
	results = append(results, t.testNodeOperations(ctx))
	fmt.Printf("🔧 DEBUG: Node operations test completed: %v\n", results[len(results)-1].Success)

	fmt.Println("🔧 DEBUG: All tests completed!")
	return results
}

// testConnection tests basic connection to etcd
func (t *EtcdTester) testConnection(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Connection Test"}

	// Simple status check
	_, err := t.client.Status(ctx, t.config.Endpoints[0])
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	return result
}

// testMemberList tests cluster member listing
func (t *EtcdTester) testMemberList(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Member List Test"}

	resp, err := t.client.MemberList(ctx)
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	members := make([]map[string]interface{}, 0, len(resp.Members))
	for _, member := range resp.Members {
		members = append(members, map[string]interface{}{
			"id":         member.ID,
			"name":       member.Name,
			"peerURLs":   member.PeerURLs,
			"clientURLs": member.ClientURLs,
		})
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"memberCount": len(resp.Members),
		"members":     members,
	}
	return result
}

// testClusterHealth tests cluster health
func (t *EtcdTester) testClusterHealth(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Cluster Health Test"}

	healthResults := make(map[string]interface{})
	allHealthy := true

	// Check each endpoint
	for _, endpoint := range t.config.Endpoints {
		status, err := t.client.Status(ctx, endpoint)
		if err != nil {
			healthResults[endpoint] = map[string]interface{}{
				"healthy": false,
				"error":   err.Error(),
			}
			allHealthy = false
			continue
		}

		healthResults[endpoint] = map[string]interface{}{
			"healthy":  true,
			"version":  status.Version,
			"dbSize":   status.DbSize,
			"leader":   status.Leader,
			"raftTerm": status.RaftTerm,
		}
	}

	result.Duration = time.Since(start)
	result.Success = allHealthy
	result.Details = healthResults

	if !allHealthy {
		result.Error = "Some endpoints are unhealthy"
	}

	return result
}

// testEndpointStatus tests individual endpoint status
func (t *EtcdTester) testEndpointStatus(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Endpoint Status Test"}

	statusResults := make(map[string]interface{})

	for _, endpoint := range t.config.Endpoints {
		status, err := t.client.Status(ctx, endpoint)
		if err != nil {
			statusResults[endpoint] = map[string]interface{}{
				"error": err.Error(),
			}
			continue
		}

		statusResults[endpoint] = map[string]interface{}{
			"version":     status.Version,
			"dbSize":      status.DbSize,
			"leader":      status.Leader,
			"raftIndex":   status.RaftIndex,
			"raftTerm":    status.RaftTerm,
			"raftApplied": status.RaftAppliedIndex,
			"errors":      status.Errors,
			"dbSizeInUse": status.DbSizeInUse,
			"isLearner":   status.IsLearner,
		}
	}

	result.Duration = time.Since(start)
	result.Success = true
	result.Details = statusResults
	return result
}

// testPut tests PUT operations (Create operations in Kubernetes)
func (t *EtcdTester) testPut(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "PUT Operation Test"}

	// Use random keys to avoid potential conflicts
	randomID := time.Now().UnixNano()
	key := fmt.Sprintf("/test/put/random-key-%d", randomID)
	value := fmt.Sprintf("test-value-%d", randomID)

	fmt.Printf("    🔧 DEBUG: About to call client.Put with RANDOM key=%s, value=%s\n", key, value)
	
	// Try with a shorter timeout first to see if it's just slow
	shortCtx, shortCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shortCancel()
	
	resp, err := t.client.Put(shortCtx, key, value)
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		fmt.Printf("    🔧 DEBUG: PUT failed after %v: %v\n", result.Duration, err)
		
		// If it failed due to timeout, try a second PUT with a different key
		fmt.Println("    🔧 DEBUG: Trying second PUT with different random key...")
		randomID2 := time.Now().UnixNano() + 1
		key2 := fmt.Sprintf("/test/put/backup-key-%d", randomID2)
		value2 := fmt.Sprintf("backup-value-%d", randomID2)
		
		shortCtx2, shortCancel2 := context.WithTimeout(ctx, 5*time.Second)
		defer shortCancel2()
		
		fmt.Printf("    🔧 DEBUG: Second PUT attempt with key=%s\n", key2)
		resp2, err2 := t.client.Put(shortCtx2, key2, value2)
		if err2 != nil {
			fmt.Printf("    🔧 DEBUG: Second PUT also failed: %v\n", err2)
		} else {
			fmt.Printf("    🔧 DEBUG: Second PUT succeeded! Revision: %d\n", resp2.Header.Revision)
		}
		
		return result
	}

	fmt.Printf("    🔧 DEBUG: PUT succeeded after %v, revision: %d\n", result.Duration, resp.Header.Revision)
	result.Success = true
	result.Details = map[string]interface{}{
		"revision": resp.Header.Revision,
		"key":      key,
		"value":    value,
	}
	return result
}

// testPutRandomKeys tests PUT operations with random keys to avoid conflicts
func (t *EtcdTester) testPutRandomKeys(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "PUT Random Keys Test"}

	var successfulPuts int
	var totalAttempts = 3
	var putResults []map[string]interface{}

	for i := 0; i < totalAttempts; i++ {
		// Use random keys to avoid potential conflicts
		randomID := time.Now().UnixNano() + int64(i)
		key := fmt.Sprintf("/test/random/key-%d-%d", randomID, i)
		value := fmt.Sprintf("random-value-%d-%d", randomID, i)

		fmt.Printf("    🔧 DEBUG: PUT attempt %d/%d with key=%s\n", i+1, totalAttempts, key)
		
		// Try with a shorter timeout for each attempt
		putCtx, putCancel := context.WithTimeout(ctx, 5*time.Second)
		
		attemptStart := time.Now()
		resp, err := t.client.Put(putCtx, key, value)
		attemptDuration := time.Since(attemptStart)
		putCancel()
		
		if err != nil {
			fmt.Printf("    🔧 DEBUG: PUT attempt %d failed after %v: %v\n", i+1, attemptDuration, err)
			putResults = append(putResults, map[string]interface{}{
				"attempt":  i + 1,
				"key":      key,
				"success":  false,
				"error":    err.Error(),
				"duration": attemptDuration.String(),
			})
		} else {
			fmt.Printf("    🔧 DEBUG: PUT attempt %d SUCCESS after %v, revision: %d\n", i+1, attemptDuration, resp.Header.Revision)
			successfulPuts++
			putResults = append(putResults, map[string]interface{}{
				"attempt":  i + 1,
				"key":      key,
				"success":  true,
				"revision": resp.Header.Revision,
				"duration": attemptDuration.String(),
			})
		}

		// Small delay between attempts
		time.Sleep(100 * time.Millisecond)
	}

	result.Duration = time.Since(start)
	
	if successfulPuts > 0 {
		result.Success = true
		fmt.Printf("    🔧 DEBUG: Random PUT test completed: %d/%d successful\n", successfulPuts, totalAttempts)
	} else {
		result.Error = fmt.Sprintf("All %d PUT attempts failed", totalAttempts)
		fmt.Printf("    🔧 DEBUG: Random PUT test FAILED: 0/%d successful\n", totalAttempts)
	}
	
	result.Details = map[string]interface{}{
		"totalAttempts":   totalAttempts,
		"successfulPuts":  successfulPuts,
		"successRate":     float64(successfulPuts) / float64(totalAttempts),
		"individualPuts":  putResults,
	}
	
	return result
}

// testGet tests GET operations (Read operations in Kubernetes)
func (t *EtcdTester) testGet(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "GET Operation Test"}

	key := "/test/put/key"

	resp, err := t.client.Get(ctx, key)
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	if len(resp.Kvs) == 0 {
		result.Error = "Key not found"
		return result
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"revision": resp.Header.Revision,
		"count":    resp.Count,
		"key":      string(resp.Kvs[0].Key),
		"value":    string(resp.Kvs[0].Value),
	}
	return result
}

// testDelete tests DELETE operations
func (t *EtcdTester) testDelete(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "DELETE Operation Test"}

	key := "/test/delete/key"
	value := "test-value-to-delete"

	// First put a key
	_, err := t.client.Put(ctx, key, value)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to put key for delete test: %v", err)
		return result
	}

	// Then delete it
	resp, err := t.client.Delete(ctx, key)
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"revision": resp.Header.Revision,
		"deleted":  resp.Deleted,
	}
	return result
}

// testTxn tests transaction operations (used for conditional updates)
func (t *EtcdTester) testTxn(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Transaction Test"}

	key := "/test/txn/key"
	value1 := "value1"
	value2 := "value2"

	// Put initial value
	_, err := t.client.Put(ctx, key, value1)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to put initial value: %v", err)
		return result
	}

	// Transaction: if key == value1, update to value2
	resp, err := t.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Value(key), "=", value1)).
		Then(clientv3.OpPut(key, value2)).
		Else(clientv3.OpGet(key)).
		Commit()

	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"succeeded": resp.Succeeded,
		"revision":  resp.Header.Revision,
	}
	return result
}

// testRange tests range operations (LIST in Kubernetes)
func (t *EtcdTester) testRange(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Range Operation Test"}

	prefix := "/test/range/"

	// Put some test data
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("%skey%d", prefix, i)
		value := fmt.Sprintf("value%d", i)
		_, err := t.client.Put(ctx, key, value)
		if err != nil {
			result.Error = fmt.Sprintf("Failed to put test data: %v", err)
			return result
		}
	}

	// Range query
	resp, err := t.client.Get(ctx, prefix, clientv3.WithPrefix())
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	keys := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		keys = append(keys, string(kv.Key))
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"count":    resp.Count,
		"keys":     keys,
		"revision": resp.Header.Revision,
	}
	return result
}

// testRangeWithLimit tests range operations with limit (pagination)
func (t *EtcdTester) testRangeWithLimit(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Range with Limit Test"}

	prefix := "/test/range/"

	// Range query with limit
	resp, err := t.client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithLimit(3))
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	keys := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		keys = append(keys, string(kv.Key))
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"count":    resp.Count,
		"keys":     keys,
		"more":     resp.More,
		"revision": resp.Header.Revision,
	}
	return result
}

// testWatch tests watch operations
func (t *EtcdTester) testWatch(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Watch Operation Test"}

	key := "/test/watch/key"
	watchChan := t.client.Watch(ctx, key)

	// Put a value in another goroutine
	go func() {
		time.Sleep(100 * time.Millisecond)
		t.client.Put(ctx, key, "watch-test-value")
	}()

	// Wait for watch event
	select {
	case watchResp := <-watchChan:
		result.Duration = time.Since(start)
		if watchResp.Err() != nil {
			result.Error = watchResp.Err().Error()
			return result
		}

		events := make([]map[string]interface{}, 0, len(watchResp.Events))
		for _, event := range watchResp.Events {
			events = append(events, map[string]interface{}{
				"type":  event.Type.String(),
				"key":   string(event.Kv.Key),
				"value": string(event.Kv.Value),
			})
		}

		result.Success = true
		result.Details = map[string]interface{}{
			"events": events,
		}

	case <-time.After(5 * time.Second):
		result.Duration = time.Since(start)
		result.Error = "Watch timeout"
	}

	return result
}

// testWatchFromRevision tests watch from specific revision
func (t *EtcdTester) testWatchFromRevision(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Watch from Revision Test"}

	key := "/test/watch/revision"

	// Put a value and get revision
	putResp, err := t.client.Put(ctx, key, "initial-value")
	if err != nil {
		result.Error = fmt.Sprintf("Failed to put initial value: %v", err)
		return result
	}

	// Watch from current revision
	watchChan := t.client.Watch(ctx, key, clientv3.WithRev(putResp.Header.Revision))

	// Update the value
	go func() {
		time.Sleep(100 * time.Millisecond)
		t.client.Put(ctx, key, "updated-value")
	}()

	// Wait for watch event
	select {
	case watchResp := <-watchChan:
		result.Duration = time.Since(start)
		if watchResp.Err() != nil {
			result.Error = watchResp.Err().Error()
			return result
		}

		result.Success = true
		result.Details = map[string]interface{}{
			"watchRevision": putResp.Header.Revision,
			"eventCount":    len(watchResp.Events),
		}

	case <-time.After(5 * time.Second):
		result.Duration = time.Since(start)
		result.Error = "Watch from revision timeout"
	}

	return result
}

// testLease tests lease operations (TTL in Kubernetes)
func (t *EtcdTester) testLease(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Lease Operation Test"}

	fmt.Println("    🔧 DEBUG: Starting lease grant operation...")
	// Grant a lease
	leaseResp, err := t.client.Grant(ctx, 60) // 60 seconds
	if err != nil {
		result.Error = fmt.Sprintf("Failed to grant lease: %v", err)
		fmt.Printf("    🔧 DEBUG: Lease grant failed: %v\n", err)
		return result
	}
	fmt.Printf("    🔧 DEBUG: Lease granted successfully! LeaseID: %d\n", leaseResp.ID)

	key := "/test/lease/key"
	value := "lease-test-value"

	fmt.Println("    🔧 DEBUG: Putting key with lease...")
	// Put with lease
	_, err = t.client.Put(ctx, key, value, clientv3.WithLease(leaseResp.ID))
	if err != nil {
		result.Error = fmt.Sprintf("Failed to put with lease: %v", err)
		fmt.Printf("    🔧 DEBUG: Put with lease failed: %v\n", err)
		return result
	}
	fmt.Println("    🔧 DEBUG: Put with lease completed successfully!")

	fmt.Println("    🔧 DEBUG: Getting TTL...")
	// Get TTL
	ttlResp, err := t.client.TimeToLive(ctx, leaseResp.ID)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to get TTL: %v", err)
		fmt.Printf("    🔧 DEBUG: Get TTL failed: %v\n", err)
		return result
	}
	fmt.Printf("    🔧 DEBUG: TTL retrieved successfully! Remaining: %d seconds\n", ttlResp.TTL)

	result.Duration = time.Since(start)
	result.Success = true
	result.Details = map[string]interface{}{
		"leaseID":      leaseResp.ID,
		"ttl":          leaseResp.TTL,
		"remainingTTL": ttlResp.TTL,
		"grantedTTL":   ttlResp.GrantedTTL,
	}
	fmt.Printf("    🔧 DEBUG: Lease test completed in %v\n", result.Duration)

	// Revoke lease
	t.client.Revoke(ctx, leaseResp.ID)

	return result
}

// testAuth tests authentication operations
func (t *EtcdTester) testAuth(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Authentication Test"}

	// Test authenticate
	authResp, err := t.client.Auth.Authenticate(ctx, t.config.Username, t.config.Password)
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"token": authResp.Token != "",
	}
	return result
}

// testCompact tests compaction operations
func (t *EtcdTester) testCompact(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Compact Operation Test"}

	// Get current revision
	resp, err := t.client.Get(ctx, "/", clientv3.WithPrefix(), clientv3.WithLimit(1))
	if err != nil {
		result.Error = fmt.Sprintf("Failed to get current revision: %v", err)
		return result
	}

	if resp.Header.Revision <= 1 {
		result.Success = true
		result.Details = map[string]interface{}{
			"message": "No compaction needed - revision too low",
		}
		return result
	}

	// Compact to previous revision
	compactResp, err := t.client.Compact(ctx, resp.Header.Revision-1)
	result.Duration = time.Since(start)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"compactRevision": resp.Header.Revision - 1,
		"header":          compactResp.Header.Revision,
	}
	return result
}

// testPerformance tests basic performance metrics
func (t *EtcdTester) testPerformance(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Performance Test"}

	numOps := 100
	prefix := "/test/perf/"

	// PUT performance
	putStart := time.Now()
	for i := 0; i < numOps; i++ {
		key := fmt.Sprintf("%skey%d", prefix, i)
		value := fmt.Sprintf("value%d", i)
		_, err := t.client.Put(ctx, key, value)
		if err != nil {
			result.Error = fmt.Sprintf("PUT failed at %d: %v", i, err)
			return result
		}
	}
	putDuration := time.Since(putStart)

	// GET performance
	getStart := time.Now()
	for i := 0; i < numOps; i++ {
		key := fmt.Sprintf("%skey%d", prefix, i)
		_, err := t.client.Get(ctx, key)
		if err != nil {
			result.Error = fmt.Sprintf("GET failed at %d: %v", i, err)
			return result
		}
	}
	getDuration := time.Since(getStart)

	result.Duration = time.Since(start)
	result.Success = true
	result.Details = map[string]interface{}{
		"operations":   numOps,
		"putTotal":     putDuration,
		"putPerOp":     putDuration / time.Duration(numOps),
		"putOpsPerSec": float64(numOps) / putDuration.Seconds(),
		"getTotal":     getDuration,
		"getPerOp":     getDuration / time.Duration(numOps),
		"getOpsPerSec": float64(numOps) / getDuration.Seconds(),
	}
	return result
}

// testKubernetesPatterns tests patterns commonly used by Kubernetes
func (t *EtcdTester) testKubernetesPatterns(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Kubernetes Patterns Test"}

	// Test patterns used by Kubernetes API Server
	testResults := make(map[string]interface{})

	// Test 1: Registry key patterns (similar to /registry/pods/default/mypod)
	kubePrefix := "/registry/pods/default/"
	podKey := kubePrefix + "test-pod"
	podValue := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test-pod","namespace":"default"}}`

	_, err := t.client.Put(ctx, podKey, podValue)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to put Kubernetes pod: %v", err)
		return result
	}

	// Test 2: List operations with prefix (LIST pods)
	listResp, err := t.client.Get(ctx, kubePrefix, clientv3.WithPrefix())
	if err != nil {
		result.Error = fmt.Sprintf("Failed to list pods: %v", err)
		return result
	}
	testResults["listPods"] = map[string]interface{}{
		"count": listResp.Count,
		"found": len(listResp.Kvs) > 0,
	}

	// Test 3: Watch events (WATCH pods)
	watchKey := kubePrefix + "watch-test-pod"
	watchChan := t.client.Watch(ctx, kubePrefix, clientv3.WithPrefix())

	go func() {
		time.Sleep(50 * time.Millisecond)
		t.client.Put(ctx, watchKey, podValue)
	}()

	select {
	case watchResp := <-watchChan:
		if watchResp.Err() != nil {
			testResults["watchPods"] = map[string]interface{}{
				"error": watchResp.Err().Error(),
			}
		} else {
			testResults["watchPods"] = map[string]interface{}{
				"eventCount": len(watchResp.Events),
				"success":    true,
			}
		}
	case <-time.After(2 * time.Second):
		testResults["watchPods"] = map[string]interface{}{
			"error": "watch timeout",
		}
	}

	// Test 4: Conditional updates (similar to resourceVersion checks)
	currentResp, err := t.client.Get(ctx, podKey)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to get current pod: %v", err)
		return result
	}

	if len(currentResp.Kvs) > 0 {
		// Conditional update based on revision (similar to resourceVersion)
		newPodValue := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test-pod","namespace":"default","resourceVersion":"2"}}`
		txnResp, err := t.client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(podKey), "=", currentResp.Kvs[0].ModRevision)).
			Then(clientv3.OpPut(podKey, newPodValue)).
			Commit()

		if err != nil {
			testResults["conditionalUpdate"] = map[string]interface{}{
				"error": err.Error(),
			}
		} else {
			testResults["conditionalUpdate"] = map[string]interface{}{
				"succeeded": txnResp.Succeeded,
				"revision":  txnResp.Header.Revision,
			}
		}
	}

	// Test 5: Event storage pattern (events have TTL)
	eventKey := "/registry/events/default/test-event"
	eventValue := `{"apiVersion":"v1","kind":"Event","metadata":{"name":"test-event","namespace":"default"}}`

	// Grant lease for event TTL
	leaseResp, err := t.client.Grant(ctx, 300) // 5 minutes
	if err == nil {
		_, err = t.client.Put(ctx, eventKey, eventValue, clientv3.WithLease(leaseResp.ID))
		if err != nil {
			testResults["eventTTL"] = map[string]interface{}{
				"error": err.Error(),
			}
		} else {
			testResults["eventTTL"] = map[string]interface{}{
				"leaseID": leaseResp.ID,
				"ttl":     leaseResp.TTL,
				"success": true,
			}
		}
	}

	result.Duration = time.Since(start)
	result.Success = true
	result.Details = testResults
	return result
}

// testNodeOperations tests node-related operations used by Kubernetes
func (t *EtcdTester) testNodeOperations(ctx context.Context) TestResult {
	start := time.Now()
	result := TestResult{Name: "Node Operations Test"}

	// Node operations tests simulate how Kubernetes handles nodes in etcd
	testResults := make(map[string]interface{})

	// Test 1: Node Registration (kubelet registers node)
	nodeName := "test-node-01"
	nodeKey := fmt.Sprintf("/registry/nodes/%s", nodeName)
	nodeSpec := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]interface{}{
			"name":              nodeName,
			"uid":               "12345678-1234-1234-1234-123456789012",
			"resourceVersion":   "1",
			"creationTimestamp": time.Now().UTC().Format(time.RFC3339),
			"labels": map[string]string{
				"kubernetes.io/hostname": nodeName,
				"kubernetes.io/arch":     "amd64",
				"kubernetes.io/os":       "linux",
			},
		},
		"spec": map[string]interface{}{
			"podCIDR": "10.244.0.0/24",
		},
		"status": map[string]interface{}{
			"capacity": map[string]string{
				"cpu":    "4",
				"memory": "8Gi",
				"pods":   "110",
			},
			"allocatable": map[string]string{
				"cpu":    "4",
				"memory": "7.5Gi",
				"pods":   "110",
			},
			"phase": "Running",
			"conditions": []map[string]interface{}{
				{
					"type":               "Ready",
					"status":             "True",
					"lastHeartbeatTime":  time.Now().UTC().Format(time.RFC3339),
					"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
					"reason":             "KubeletReady",
					"message":            "kubelet is posting ready status",
				},
			},
			"addresses": []map[string]interface{}{
				{
					"type":    "InternalIP",
					"address": "192.168.1.100",
				},
				{
					"type":    "Hostname",
					"address": nodeName,
				},
			},
		},
	}

	nodeJSON, _ := json.Marshal(nodeSpec)
	_, err := t.client.Put(ctx, nodeKey, string(nodeJSON))
	if err != nil {
		testResults["nodeRegistration"] = map[string]interface{}{
			"error": fmt.Sprintf("Failed to register node: %v", err),
		}
	} else {
		testResults["nodeRegistration"] = map[string]interface{}{
			"success": true,
			"nodeKey": nodeKey,
		}
	}

	// Test 2: Node Status Updates (kubelet heartbeats)
	for i := 0; i < 3; i++ {
		// Update node status with new heartbeat
		nodeSpec["status"].(map[string]interface{})["conditions"].([]map[string]interface{})[0]["lastHeartbeatTime"] = time.Now().UTC().Format(time.RFC3339)
		nodeSpec["metadata"].(map[string]interface{})["resourceVersion"] = fmt.Sprintf("%d", i+2)

		updatedNodeJSON, _ := json.Marshal(nodeSpec)

		// Conditional update based on resource version (optimistic concurrency)
		currentResp, err := t.client.Get(ctx, nodeKey)
		if err == nil && len(currentResp.Kvs) > 0 {
			txnResp, err := t.client.Txn(ctx).
				If(clientv3.Compare(clientv3.ModRevision(nodeKey), "=", currentResp.Kvs[0].ModRevision)).
				Then(clientv3.OpPut(nodeKey, string(updatedNodeJSON))).
				Commit()

			if err != nil {
				testResults[fmt.Sprintf("nodeHeartbeat_%d", i+1)] = map[string]interface{}{
					"error": err.Error(),
				}
			} else {
				testResults[fmt.Sprintf("nodeHeartbeat_%d", i+1)] = map[string]interface{}{
					"success":   txnResp.Succeeded,
					"revision":  txnResp.Header.Revision,
					"heartbeat": i + 1,
				}
			}
		}

		time.Sleep(50 * time.Millisecond) // Simulate heartbeat interval
	}

	// Test 3: Node Lease Operations (node heartbeat via leases - newer K8s versions)
	leaseKey := fmt.Sprintf("/registry/leases/kube-node-lease/%s", nodeName)
	leaseSpec := map[string]interface{}{
		"apiVersion": "coordination.k8s.io/v1",
		"kind":       "Lease",
		"metadata": map[string]interface{}{
			"name":      nodeName,
			"namespace": "kube-node-lease",
		},
		"spec": map[string]interface{}{
			"holderIdentity":       nodeName,
			"leaseDurationSeconds": 40,
			"renewTime":            time.Now().UTC().Format(time.RFC3339Nano),
		},
	}

	leaseJSON, _ := json.Marshal(leaseSpec)

	// Create lease with TTL
	leaseResp, err := t.client.Grant(ctx, 60) // 60 seconds TTL
	if err != nil {
		testResults["nodeLease"] = map[string]interface{}{
			"error": fmt.Sprintf("Failed to grant lease: %v", err),
		}
	} else {
		_, err = t.client.Put(ctx, leaseKey, string(leaseJSON), clientv3.WithLease(leaseResp.ID))
		if err != nil {
			testResults["nodeLease"] = map[string]interface{}{
				"error": fmt.Sprintf("Failed to create node lease: %v", err),
			}
		} else {
			// Simulate lease renewals (heartbeats)
			renewCount := 0
			for j := 0; j < 3; j++ {
				leaseSpec["spec"].(map[string]interface{})["renewTime"] = time.Now().UTC().Format(time.RFC3339Nano)
				renewedLeaseJSON, _ := json.Marshal(leaseSpec)

				_, err = t.client.Put(ctx, leaseKey, string(renewedLeaseJSON), clientv3.WithLease(leaseResp.ID))
				if err == nil {
					renewCount++
				}
				time.Sleep(30 * time.Millisecond)
			}

			ttlResp, _ := t.client.TimeToLive(ctx, leaseResp.ID)
			testResults["nodeLease"] = map[string]interface{}{
				"success":      true,
				"leaseID":      leaseResp.ID,
				"renewals":     renewCount,
				"remainingTTL": ttlResp.TTL,
			}
		}
	}

	// Test 4: Node List Operations (scheduler/controller queries)
	listResp, err := t.client.Get(ctx, "/registry/nodes/", clientv3.WithPrefix())
	if err != nil {
		testResults["nodeList"] = map[string]interface{}{
			"error": err.Error(),
		}
	} else {
		nodes := make([]string, 0)
		for _, kv := range listResp.Kvs {
			keyParts := strings.Split(string(kv.Key), "/")
			if len(keyParts) >= 3 {
				nodes = append(nodes, keyParts[len(keyParts)-1])
			}
		}
		testResults["nodeList"] = map[string]interface{}{
			"success":   true,
			"nodeCount": len(nodes),
			"nodes":     nodes,
		}
	}

	// Test 5: Node Watch Operations (controller manager watches nodes)
	nodeWatchKey := "/registry/nodes/" + nodeName + "-watch"
	watchChan := t.client.Watch(ctx, "/registry/nodes/", clientv3.WithPrefix())

	go func() {
		time.Sleep(100 * time.Millisecond)
		// Simulate node status change
		nodeSpec["status"].(map[string]interface{})["conditions"].([]map[string]interface{})[0]["status"] = "False"
		nodeSpec["status"].(map[string]interface{})["conditions"].([]map[string]interface{})[0]["reason"] = "KubeletNotReady"
		nodeSpec["status"].(map[string]interface{})["conditions"].([]map[string]interface{})[0]["message"] = "kubelet is not ready"

		nodeChangeJSON, _ := json.Marshal(nodeSpec)
		t.client.Put(ctx, nodeWatchKey, string(nodeChangeJSON))
	}()

	select {
	case watchResp := <-watchChan:
		if watchResp.Err() != nil {
			testResults["nodeWatch"] = map[string]interface{}{
				"error": watchResp.Err().Error(),
			}
		} else {
			events := make([]map[string]interface{}, 0)
			for _, event := range watchResp.Events {
				if strings.Contains(string(event.Kv.Key), "nodes") {
					events = append(events, map[string]interface{}{
						"type": event.Type.String(),
						"key":  string(event.Kv.Key),
					})
				}
			}
			testResults["nodeWatch"] = map[string]interface{}{
				"success":    true,
				"events":     events,
				"eventCount": len(events),
			}
		}
	case <-time.After(3 * time.Second):
		testResults["nodeWatch"] = map[string]interface{}{
			"timeout": true,
			"message": "No node events detected within timeout",
		}
	}

	// Test 6: Node Annotation Updates (common controller operations)
	annotationKey := nodeKey
	// Get current node
	currentNodeResp, err := t.client.Get(ctx, annotationKey)
	if err == nil && len(currentNodeResp.Kvs) > 0 {
		var currentNode map[string]interface{}
		json.Unmarshal(currentNodeResp.Kvs[0].Value, &currentNode)

		// Add annotations (common controller operation)
		if metadata, ok := currentNode["metadata"].(map[string]interface{}); ok {
			if metadata["annotations"] == nil {
				metadata["annotations"] = make(map[string]interface{})
			}
			annotations := metadata["annotations"].(map[string]interface{})
			annotations["node.alpha.kubernetes.io/ttl"] = "0"
			annotations["controller.kubernetes.io/node-cidr"] = "10.244.0.0/24"
			annotations["node.alpha.kubernetes.io/test-annotation"] = "test-value"
		}

		annotatedNodeJSON, _ := json.Marshal(currentNode)

		// Conditional update
		txnResp, err := t.client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(annotationKey), "=", currentNodeResp.Kvs[0].ModRevision)).
			Then(clientv3.OpPut(annotationKey, string(annotatedNodeJSON))).
			Commit()

		if err != nil {
			testResults["nodeAnnotations"] = map[string]interface{}{
				"error": err.Error(),
			}
		} else {
			testResults["nodeAnnotations"] = map[string]interface{}{
				"success":  txnResp.Succeeded,
				"revision": txnResp.Header.Revision,
			}
		}
	}

	// Test 7: Node Taint Operations (scheduler uses this)
	taintKey := nodeKey
	currentTaintResp, err := t.client.Get(ctx, taintKey)
	if err == nil && len(currentTaintResp.Kvs) > 0 {
		var currentNode map[string]interface{}
		json.Unmarshal(currentTaintResp.Kvs[0].Value, &currentNode)

		// Add taints
		if spec, ok := currentNode["spec"].(map[string]interface{}); ok {
			spec["taints"] = []map[string]interface{}{
				{
					"key":    "node.kubernetes.io/not-ready",
					"value":  "",
					"effect": "NoSchedule",
				},
				{
					"key":    "node.kubernetes.io/unreachable",
					"value":  "",
					"effect": "NoExecute",
				},
			}
		}

		taintedNodeJSON, _ := json.Marshal(currentNode)

		txnResp, err := t.client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(taintKey), "=", currentTaintResp.Kvs[0].ModRevision)).
			Then(clientv3.OpPut(taintKey, string(taintedNodeJSON))).
			Commit()

		if err != nil {
			testResults["nodeTaints"] = map[string]interface{}{
				"error": err.Error(),
			}
		} else {
			testResults["nodeTaints"] = map[string]interface{}{
				"success":  txnResp.Succeeded,
				"revision": txnResp.Header.Revision,
			}
		}
	}

	// Test 8: Node Deletion (node drain/removal)
	deleteNodeKey := "/registry/nodes/test-node-to-delete"
	tempNodeJSON, _ := json.Marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]interface{}{
			"name": "test-node-to-delete",
		},
	})

	// Create temporary node
	_, err = t.client.Put(ctx, deleteNodeKey, string(tempNodeJSON))
	if err == nil {
		// Delete node
		deleteResp, err := t.client.Delete(ctx, deleteNodeKey)
		if err != nil {
			testResults["nodeDeletion"] = map[string]interface{}{
				"error": err.Error(),
			}
		} else {
			testResults["nodeDeletion"] = map[string]interface{}{
				"success":  true,
				"deleted":  deleteResp.Deleted,
				"revision": deleteResp.Header.Revision,
			}
		}
	}

	// Test 9: Node Allocation Tracking (resourceVersion consistency)
	allocationKey := "/registry/nodes/" + nodeName

	// Simulate multiple controllers updating node simultaneously
	var successfulUpdates int
	var conflictErrors int

	for i := 0; i < 5; i++ {
		go func(updateID int) {
			currentResp, err := t.client.Get(ctx, allocationKey)
			if err == nil && len(currentResp.Kvs) > 0 {
				var node map[string]interface{}
				json.Unmarshal(currentResp.Kvs[0].Value, &node)

				// Simulate allocation update
				if status, ok := node["status"].(map[string]interface{}); ok {
					if allocated, ok := status["allocatable"].(map[string]string); ok {
						// Simulate pod allocation
						allocated[fmt.Sprintf("example.com/resource-%d", updateID)] = "1"
					}
				}

				updatedJSON, _ := json.Marshal(node)

				txnResp, err := t.client.Txn(ctx).
					If(clientv3.Compare(clientv3.ModRevision(allocationKey), "=", currentResp.Kvs[0].ModRevision)).
					Then(clientv3.OpPut(allocationKey, string(updatedJSON))).
					Commit()

				if err == nil && txnResp.Succeeded {
					successfulUpdates++
				} else {
					conflictErrors++
				}
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond) // Allow goroutines to complete

	testResults["nodeAllocation"] = map[string]interface{}{
		"successfulUpdates": successfulUpdates,
		"conflictErrors":    conflictErrors,
		"totalAttempts":     5,
	}

	// Test 10: Node Status Summary
	nodeStatusResp, err := t.client.Get(ctx, "/registry/nodes/", clientv3.WithPrefix())
	if err == nil {
		var readyNodes, notReadyNodes int
		for _, kv := range nodeStatusResp.Kvs {
			var node map[string]interface{}
			json.Unmarshal(kv.Value, &node)

			if status, ok := node["status"].(map[string]interface{}); ok {
				if conditions, ok := status["conditions"].([]interface{}); ok {
					for _, cond := range conditions {
						if condition, ok := cond.(map[string]interface{}); ok {
							if condition["type"] == "Ready" {
								if condition["status"] == "True" {
									readyNodes++
								} else {
									notReadyNodes++
								}
								break
							}
						}
					}
				}
			}
		}

		testResults["nodeStatusSummary"] = map[string]interface{}{
			"totalNodes":    len(nodeStatusResp.Kvs),
			"readyNodes":    readyNodes,
			"notReadyNodes": notReadyNodes,
		}
	}

	result.Duration = time.Since(start)
	result.Success = true
	result.Details = testResults
	return result
}

// printResults prints test results in a formatted way
func printResults(results []TestResult) {
	fmt.Println("\n=== Etcd Test Results ===")
	fmt.Println()

	totalTests := len(results)
	passedTests := 0

	for _, result := range results {
		status := "❌ FAIL"
		if result.Success {
			status = "✅ PASS"
			passedTests++
		}

		fmt.Printf("%s %s (%.2fms)\n", status, result.Name, float64(result.Duration.Nanoseconds())/1000000)

		if result.Error != "" {
			fmt.Printf("   Error: %s\n", result.Error)
		}

		if result.Details != nil {
			detailsJSON, _ := json.MarshalIndent(result.Details, "   ", "  ")
			fmt.Printf("   Details: %s\n", string(detailsJSON))
		}
		fmt.Println()
	}

	fmt.Printf("Summary: %d/%d tests passed (%.1f%%)\n", passedTests, totalTests, float64(passedTests)/float64(totalTests)*100)
}

func (t *EtcdTester) testPerformanceBasic() TestResult {
	result := TestResult{Name: "Performance Basic Test"}
	start := time.Now()

	var wg sync.WaitGroup
	numOperations := 100
	results := make(chan error, numOperations)

	// Launch concurrent operations
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			key := fmt.Sprintf("/perf-test/key-%d", index)
			value := fmt.Sprintf("value-%d-%d", index, time.Now().UnixNano())

			// PUT operation
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := t.client.Put(ctx, key, value)
			results <- err
		}(i)
	}

	// Wait for all operations to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Count errors
	errorCount := 0
	for err := range results {
		if err != nil {
			errorCount++
		}
	}

	duration := time.Since(start)
	result.Duration = duration

	if errorCount > 0 {
		result.Success = false
		result.Error = fmt.Sprintf("failed with %d errors out of %d operations", errorCount, numOperations)
		return result
	}

	result.Success = true
	result.Details = map[string]interface{}{
		"operations":  numOperations,
		"duration_ms": duration.Milliseconds(),
		"ops_per_sec": float64(numOperations) / duration.Seconds(),
		"errors":      errorCount,
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.client.Delete(ctx, "/perf-test/", clientv3.WithPrefix())

	return result
}

func main() {
	// Check if this is a performance test command
	if len(os.Args) > 1 && os.Args[1] == "perf" {
		runPerfCommand()
		return
	}

	// Default configuration
	config := &TestConfig{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
	}

	// Parse command line arguments
	if len(os.Args) > 1 {
		config.Endpoints = strings.Split(os.Args[1], ",")
	}

	// Check for TLS certificates in environment
	if caFile := os.Getenv("ETCD_CA_FILE"); caFile != "" {
		config.CAFile = caFile
	}
	if certFile := os.Getenv("ETCD_CERT_FILE"); certFile != "" {
		config.CertFile = certFile
	}
	if keyFile := os.Getenv("ETCD_KEY_FILE"); keyFile != "" {
		config.KeyFile = keyFile
	}

	// Check for authentication
	if username := os.Getenv("ETCD_USERNAME"); username != "" {
		config.Username = username
	}
	if password := os.Getenv("ETCD_PASSWORD"); password != "" {
		config.Password = password
	}

	fmt.Printf("Testing etcd endpoints: %v\n", config.Endpoints)
	if config.CAFile != "" {
		fmt.Printf("Using TLS with CA: %s\n", config.CAFile)
	}
	if config.Username != "" {
		fmt.Printf("Using authentication for user: %s\n", config.Username)
	}

	fmt.Println("🔧 DEBUG: Creating etcd tester...")
	// Create tester
	tester, err := NewEtcdTester(config)
	if err != nil {
		log.Fatalf("Failed to create etcd tester: %v", err)
	}
	defer tester.Close()
	fmt.Println("🔧 DEBUG: Etcd tester created successfully!")

	fmt.Println("🔧 DEBUG: Starting tests with 30s timeout...")
	// Run tests
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results := tester.RunAllTests(ctx)
	fmt.Println("🔧 DEBUG: All tests completed, printing results...")
	printResults(results)

	// Exit with error code if any tests failed
	for _, result := range results {
		if !result.Success {
			os.Exit(1)
		}
	}
}

func runPerfCommand() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: etcd-tester perf <etcd-endpoint> [node-count] [duration-minutes]")
		fmt.Println("Example: etcd-tester perf localhost:2379 100 5")
		fmt.Println("Example: etcd-tester perf localhost:2379 100 0  (run indefinitely until Ctrl+C)")
		os.Exit(1)
	}

	endpoint := os.Args[2]
	nodeCount := 100
	durationMinutes := 5

	if len(os.Args) >= 4 {
		if count, err := strconv.Atoi(os.Args[3]); err == nil {
			nodeCount = count
		}
	}

	if len(os.Args) >= 5 {
		if mins, err := strconv.Atoi(os.Args[4]); err == nil {
			durationMinutes = mins
		}
	}

	duration := time.Duration(durationMinutes) * time.Minute
	if durationMinutes == 0 {
		fmt.Printf("Starting etcd node performance test with %d nodes against %s (run indefinitely - press Ctrl+C to stop)\n", nodeCount, endpoint)
	} else {
		fmt.Printf("Starting etcd node performance test with %d nodes against %s for %v (press Ctrl+C to stop early)\n", nodeCount, endpoint, duration)
	}

	if err := RunNodePerfTest(endpoint, nodeCount, duration); err != nil {
		fmt.Printf("Performance test failed: %v\n", err)
		os.Exit(1)
	}
}
