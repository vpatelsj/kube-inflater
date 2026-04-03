package watchstress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"kube-inflater/internal/resourcegen"
)

var stressItemGVR = schema.GroupVersionResource{
	Group:    resourcegen.CRDGroup,
	Version:  resourcegen.CRDVersion,
	Resource: resourcegen.CRDPlural,
}

// Mutator continuously creates, updates, and deletes StressItems to generate
// watch events for concurrent watch connections.
type Mutator struct {
	dynClient dynamic.Interface
	cfg       MutatorConfig
}

// NewMutator creates a new resource mutator.
func NewMutator(dynClient dynamic.Interface, cfg MutatorConfig) *Mutator {
	return &Mutator{dynClient: dynClient, cfg: cfg}
}

// Run executes the mutation loop for the configured duration, returning a summary.
// The cycle is: create a batch → update each item → delete the batch → repeat.
func (m *Mutator) Run(ctx context.Context) (*MutatorSummary, error) {
	logInfo(fmt.Sprintf("Starting mutator: rate=%d/s, duration=%v, batch=%d, namespaces=%d",
		m.cfg.Rate, m.cfg.Duration, m.cfg.BatchSize, m.cfg.SpreadCount))

	ctx, cancel := context.WithTimeout(ctx, m.cfg.Duration)
	defer cancel()

	var creates, updates, deletes, errors atomic.Int64
	start := time.Now()

	// Rate limiter: if rate > 0, use a ticker; otherwise go as fast as possible.
	var ticker *time.Ticker
	if m.cfg.Rate > 0 {
		ticker = time.NewTicker(time.Second / time.Duration(m.cfg.Rate))
		defer ticker.Stop()
	}

	wait := func() bool {
		if ticker == nil {
			return ctx.Err() == nil
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			return true
		}
	}

	cycle := 0
	for ctx.Err() == nil {
		nsIdx := cycle % m.cfg.SpreadCount
		ns := fmt.Sprintf("%s-%d", m.cfg.Namespace, nsIdx)

		// Phase 1: Create a batch
		var names []string
		for i := 0; i < m.cfg.BatchSize; i++ {
			if !wait() {
				break
			}
			name := fmt.Sprintf("mut-%d-%d", cycle, i)
			obj := m.buildStressItem(name, ns, cycle)
			_, err := m.dynClient.Resource(stressItemGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				errors.Add(1)
				continue
			}
			creates.Add(1)
			names = append(names, name)
		}

		// Phase 2: Update each item in the batch
		for _, name := range names {
			if !wait() {
				break
			}
			patch := fmt.Sprintf(`{"metadata":{"annotations":{"mutated-at":"%s"}},"spec":{"payload":"%s"}}`,
				time.Now().Format(time.RFC3339Nano), m.randomPayload(64))
			_, err := m.dynClient.Resource(stressItemGVR).Namespace(ns).Patch(
				ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				errors.Add(1)
				continue
			}
			updates.Add(1)
		}

		// Phase 3: Delete the batch
		for _, name := range names {
			if !wait() {
				break
			}
			err := m.dynClient.Resource(stressItemGVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				errors.Add(1)
				continue
			}
			deletes.Add(1)
		}

		cycle++
	}

	elapsed := time.Since(start)
	totalOps := creates.Load() + updates.Load() + deletes.Load()

	return &MutatorSummary{
		Creates:    creates.Load(),
		Updates:    updates.Load(),
		Deletes:    deletes.Load(),
		Errors:     errors.Load(),
		Duration:   elapsed.Seconds(),
		ActualRate: float64(totalOps) / elapsed.Seconds(),
	}, nil
}

func (m *Mutator) buildStressItem(name, namespace string, cycle int) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": fmt.Sprintf("%s/%s", resourcegen.CRDGroup, resourcegen.CRDVersion),
			"kind":       resourcegen.CRDKind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app": resourcegen.AppLabel,
				},
				"annotations": map[string]interface{}{
					"mutated-at": time.Now().Format(time.RFC3339Nano),
				},
			},
			"spec": map[string]interface{}{
				"payload": m.randomPayload(m.cfg.DataSizeBytes),
				"index":   int64(cycle),
			},
		},
	}
}

func (m *Mutator) randomPayload(size int) string {
	if size <= 0 {
		size = 64
	}
	raw := make([]byte, (size+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%0*d", size, 0)
	}
	s := hex.EncodeToString(raw)
	if len(s) > size {
		s = s[:size]
	}
	return s
}
