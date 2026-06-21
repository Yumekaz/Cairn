package config

import (
	"os"
	"path/filepath"

	"github.com/yumekaz/cairn/internal/api"
	"gopkg.in/yaml.v3"
)

// DaemonConfig holds configuration settings for the cairnd daemon.
type DaemonConfig struct {
	SocketPath       string `yaml:"socket_path"`
	DatabasePath     string `yaml:"database_path"`
	DataDir          string `yaml:"data_dir"`
	VolumeDir        string `yaml:"volume_dir"`
	BackupDir        string `yaml:"backup_dir"`
	MiniDockerSocket string `yaml:"mini_docker_socket"`
	DashboardAddr    string `yaml:"dashboard_addr"`
}

// DefaultConfig returns the default configuration based on environment and disk availability.
func DefaultConfig() *DaemonConfig {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/home/yumekaz" // Fallback
	}

	cairnDir := filepath.Join(homeDir, ".cairn")
	socketPath := filepath.Join(cairnDir, "cairnd.sock")
	dbPath := filepath.Join(cairnDir, "cairn.db")

	// Default to local directories
	volumeDir := filepath.Join(cairnDir, "volumes")
	backupDir := filepath.Join(cairnDir, "backups")

	// Detect if HDD mount point is writable
	hddPath := "/mnt/workspace"
	if info, err := os.Stat(hddPath); err == nil && info.IsDir() {
		// Verify write access by trying to create a test directory
		testDir := filepath.Join(hddPath, ".cairn-test")
		if err := os.Mkdir(testDir, 0755); err == nil {
			os.Remove(testDir)
			// Mount point is writable! Use it for volumes and backups
			volumeDir = filepath.Join(hddPath, "volumes")
			backupDir = filepath.Join(hddPath, "backups")
		}
	}

	// Determine Mini-Docker socket
	miniDockerSocket := "/run/user/1000/mini-docker/mini-docker.sock"
	if uid := os.Geteuid(); uid == 0 {
		miniDockerSocket = "/var/run/mini-docker/mini-docker.sock"
	} else if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		miniDockerSocket = filepath.Join(xdgRuntime, "mini-docker", "mini-docker.sock")
	}

	return &DaemonConfig{
		SocketPath:       socketPath,
		DatabasePath:     dbPath,
		DataDir:          cairnDir,
		VolumeDir:        volumeDir,
		BackupDir:        backupDir,
		MiniDockerSocket: miniDockerSocket,
		DashboardAddr:    "127.0.0.1:2476",
	}
}

// LoadDaemonConfig loads the daemon configuration from the specified path, falling back to defaults.
func LoadDaemonConfig(path string) (*DaemonConfig, error) {
	cfg := DefaultConfig()

	if path == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			defaultPath := filepath.Join(homeDir, ".cairn", "cairnd-config.yaml")
			if _, err := os.Stat(defaultPath); err == nil {
				path = defaultPath
			}
		}
	}

	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// SaveDaemonConfig saves the daemon configuration to a file.
func SaveDaemonConfig(cfg *DaemonConfig, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := yaml.NewEncoder(file)
	return encoder.Encode(cfg)
}

// ParseServiceConfig parses a service definition yaml (cairn.yaml).
func ParseServiceConfig(path string) (*api.ServiceConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cfg api.ServiceConfig
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
