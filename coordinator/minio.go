// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MinIOConfig holds MinIO server configuration
type MinIOConfig struct {
	Endpoint     string // e.g., "https://coordinator.exe.xyz:9000"
	AdminUser    string
	AdminPass    string
	MCPath       string // path to mc binary
	Bucket       string // default: "shared"
	WorkerPolicy string // policy name for workers
}

// MinIOManager handles worker credential management
type MinIOManager struct {
	config MinIOConfig
	mu     sync.Mutex
}

// MinIOCredentials are returned to workers
type MinIOCredentials struct {
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
}

// NewMinIOManager creates a new MinIO manager
func NewMinIOManager(config MinIOConfig) *MinIOManager {
	if config.Bucket == "" {
		config.Bucket = "shared"
	}
	if config.WorkerPolicy == "" {
		config.WorkerPolicy = "worker-policy"
	}
	return &MinIOManager{config: config}
}

// generateMinIOPassword creates a secure random password
func generateMinIOPassword() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// CreateWorkerCredentials creates or updates credentials for a worker
func (m *MinIOManager) CreateWorkerCredentials(workerID string) (*MinIOCredentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	password := generateMinIOPassword()

	// Use context with timeout for mc commands
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Remove existing user if present (--insecure for self-signed certs)
	removeCmd := exec.CommandContext(ctx, m.config.MCPath, "--insecure", "admin", "user", "remove", "local", workerID)
	removeCmd.Run() // Ignore error - user may not exist

	// Create new user
	cmd := exec.CommandContext(ctx, m.config.MCPath, "--insecure", "admin", "user", "add", "local", workerID, password)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to create user: %s - %w", string(output), err)
	}

	// Attach policy
	cmd = exec.CommandContext(ctx, m.config.MCPath, "--insecure", "admin", "policy", "attach", "local", m.config.WorkerPolicy, "--user", workerID)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to attach policy: %s - %w", string(output), err)
	}

	return &MinIOCredentials{
		Endpoint:  m.config.Endpoint,
		AccessKey: workerID,
		SecretKey: password,
		Bucket:    m.config.Bucket,
	}, nil
}

// DeleteWorkerCredentials removes a worker's credentials
func (m *MinIOManager) DeleteWorkerCredentials(workerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := exec.Command(m.config.MCPath, "--insecure", "admin", "user", "remove", "local", workerID)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove user: %s - %w", string(output), err)
	}
	return nil
}

// HandleMinIOCredentials is the HTTP handler for /api/minio-creds
func (m *MinIOManager) HandleMinIOCredentials(w http.ResponseWriter, r *http.Request) {
	workerID := r.URL.Query().Get("worker_id")
	if workerID == "" {
		http.Error(w, `{"error": "worker_id parameter required"}`, http.StatusBadRequest)
		return
	}

	// Sanitize worker ID (alphanumeric and hyphens only)
	for _, c := range workerID {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			http.Error(w, `{"error": "invalid worker_id"}`, http.StatusBadRequest)
			return
		}
	}

	creds, err := m.CreateWorkerCredentials(workerID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(creds)
}

// LoadMinIOConfigFromFile loads config from .minio-credentials file
func LoadMinIOConfigFromFile(minioDir string) (*MinIOConfig, error) {
	credsFile := filepath.Join(minioDir, ".minio-credentials")
	data, err := os.ReadFile(credsFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", credsFile, err)
	}

	config := &MinIOConfig{
		MCPath:       filepath.Join(minioDir, "mc"),
		Bucket:       "shared",
		WorkerPolicy: "worker-policy",
	}

	// Parse shell-style exports
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimPrefix(line, "export ")
		}
		if strings.HasPrefix(line, "MINIO_ROOT_USER=") {
			config.AdminUser = strings.Trim(strings.TrimPrefix(line, "MINIO_ROOT_USER="), "\"")
		}
		if strings.HasPrefix(line, "MINIO_ROOT_PASSWORD=") {
			config.AdminPass = strings.Trim(strings.TrimPrefix(line, "MINIO_ROOT_PASSWORD="), "\"")
		}
	}

	if config.AdminUser == "" || config.AdminPass == "" {
		return nil, fmt.Errorf("missing MINIO_ROOT_USER or MINIO_ROOT_PASSWORD in %s", credsFile)
	}

	return config, nil
}

// InitMinIO initializes MinIO manager from config directory
// Returns nil if MinIO is not configured
func InitMinIO(minioDir, coordHost string, minioPort int) *MinIOManager {
	config, err := LoadMinIOConfigFromFile(minioDir)
	if err != nil {
		log.Printf("MinIO not configured: %v", err)
		return nil
	}

	config.Endpoint = fmt.Sprintf("https://%s:%d", coordHost, minioPort)

	log.Printf("MinIO credential endpoint enabled")
	log.Printf("MinIO endpoint: %s", config.Endpoint)
	log.Printf("MinIO bucket: %s", config.Bucket)

	return NewMinIOManager(*config)
}
