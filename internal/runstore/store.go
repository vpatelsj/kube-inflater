package runstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	Namespace  = "benchmark-ui"
	labelApp   = "benchmark-ui"
	labelRunID = "benchmark-ui/run-id"
	dataKey    = "run.json"
	logsKey    = "logs.json"
	// ConfigMap data limit is ~1 MiB; cap persisted logs to stay well under.
	maxPersistedLogLines = 2000
)

// PersistedRun is the JSON-serialisable subset of a Run that we store
// in a ConfigMap.  It deliberately omits process handles and mutexes.
type PersistedRun struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	StartedAt string          `json:"startedAt"`
	EndedAt   string          `json:"endedAt,omitempty"`
	Config    json.RawMessage `json:"config"`
	ReportID  string          `json:"reportID,omitempty"`
	Error     string          `json:"error,omitempty"`
	CLIRunID  string          `json:"cliRunID,omitempty"`
	LogLines  []string        `json:"logLines,omitempty"`
}

// Store persists run metadata as ConfigMaps in a dedicated namespace.
type Store struct {
	client kubernetes.Interface
}

// New creates a Store, using in-cluster config first, then falling back to kubeconfig.
func New() (*Store, error) {
	cfg, err := loadKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("runstore: loading kubeconfig: %w", err)
	}
	cfg.QPS = 20
	cfg.Burst = 40

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("runstore: creating k8s client: %w", err)
	}

	// Ensure namespace exists
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = client.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace}}
		if _, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("runstore: creating namespace %s: %w", Namespace, err)
		}
		log.Printf("runstore: created namespace %s", Namespace)
	} else if err != nil {
		return nil, fmt.Errorf("runstore: checking namespace: %w", err)
	}

	return &Store{client: client}, nil
}

// cmName produces a deterministic ConfigMap name from a run ID.
func cmName(runID string) string {
	// run IDs look like "run-20260409-195229-1234"; keep it as-is (valid DNS subdomain).
	return runID
}

// Save creates or updates the ConfigMap for this run.
func (s *Store) Save(pr *PersistedRun) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Truncate logs for persistence
	logs := pr.LogLines
	if len(logs) > maxPersistedLogLines {
		logs = logs[len(logs)-maxPersistedLogLines:]
	}
	logsJSON, _ := json.Marshal(logs)

	runJSON, err := json.Marshal(pr)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName(pr.ID),
			Namespace: Namespace,
			Labels: map[string]string{
				"app":      labelApp,
				labelRunID: pr.ID,
			},
		},
		Data: map[string]string{
			dataKey: string(runJSON),
			logsKey: string(logsJSON),
		},
	}

	// Try update first; create if not found
	existing, err := s.client.CoreV1().ConfigMaps(Namespace).Get(ctx, cm.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = s.client.CoreV1().ConfigMaps(Namespace).Create(ctx, cm, metav1.CreateOptions{})
	} else if err == nil {
		existing.Data = cm.Data
		existing.Labels = cm.Labels
		_, err = s.client.CoreV1().ConfigMaps(Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("save run %s: %w", pr.ID, err)
	}
	return nil
}

// LoadAll returns every persisted run from the namespace.
func (s *Store) LoadAll() ([]*PersistedRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list, err := s.client.CoreV1().ConfigMaps(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + labelApp,
	})
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}

	var runs []*PersistedRun
	for _, cm := range list.Items {
		raw, ok := cm.Data[dataKey]
		if !ok {
			continue
		}
		pr := &PersistedRun{}
		if err := json.Unmarshal([]byte(raw), pr); err != nil {
			log.Printf("runstore: skipping malformed ConfigMap %s: %v", cm.Name, err)
			continue
		}
		// Restore logs from separate key (may be larger/truncated independently)
		if logsRaw, ok := cm.Data[logsKey]; ok {
			var logs []string
			if err := json.Unmarshal([]byte(logsRaw), &logs); err == nil {
				pr.LogLines = logs
			}
		}
		runs = append(runs, pr)
	}
	return runs, nil
}

// Delete removes the ConfigMap for a run.
func (s *Store) Delete(runID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := s.client.CoreV1().ConfigMaps(Namespace).Delete(ctx, cmName(runID), metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

func loadKubeConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}

// TruncateForPersistence returns a copy of LogLines capped to the
// persistence limit.  Callers should NOT modify the returned slice.
func TruncateForPersistence(lines []string) []string {
	if len(lines) <= maxPersistedLogLines {
		return lines
	}
	return lines[len(lines)-maxPersistedLogLines:]
}

// SanitizeRunID strips characters that are not valid in a ConfigMap name.
func SanitizeRunID(id string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return '-'
	}, id)
}
