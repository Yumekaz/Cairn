package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		homeDir = os.TempDir() // last-resort fallback (never hardcode a username path)
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

// ResolveServiceConfigPath accepts either a cairn.yaml file path or a directory
// containing cairn.yaml and returns the path to the yaml file.
func ResolveServiceConfigPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		candidate := filepath.Join(path, "cairn.yaml")
		if _, err := os.Stat(candidate); err != nil {
			return "", fmt.Errorf("directory %s does not contain cairn.yaml: %w", path, err)
		}
		return candidate, nil
	}
	return path, nil
}

// ResolveImagePath expands a service image/rootfs path. Relative paths are
// resolved against the config file directory, then the process working
// directory. Environment overrides:
//   - CAIRN_ROOTFS: used when image is empty or the literal "${CAIRN_ROOTFS}"
//   - MINI_DOCKER_ROOTFS: fallback if CAIRN_ROOTFS is unset
func ResolveImagePath(image string, configFilePath string) string {
	image = strings.TrimSpace(image)
	if image == "" || image == "${CAIRN_ROOTFS}" || image == "$CAIRN_ROOTFS" {
		if v := os.Getenv("CAIRN_ROOTFS"); v != "" {
			return v
		}
		if v := os.Getenv("MINI_DOCKER_ROOTFS"); v != "" {
			return v
		}
		return image
	}
	if filepath.IsAbs(image) {
		return image
	}
	// Prefer path relative to the yaml file (portable examples).
	if configFilePath != "" {
		base := filepath.Dir(configFilePath)
		candidate := filepath.Join(base, image)
		if _, err := os.Stat(candidate); err == nil {
			abs, err := filepath.Abs(candidate)
			if err == nil {
				return abs
			}
			return candidate
		}
	}
	if abs, err := filepath.Abs(image); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return image
}

// ParseServiceConfig parses a service definition yaml (cairn.yaml).
// path may be a file or a directory containing cairn.yaml.
func ParseServiceConfig(path string) (*api.ServiceConfig, error) {
	resolved, err := ResolveServiceConfigPath(path)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cfg api.ServiceConfig
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}

	cfg.Image = ResolveImagePath(cfg.Image, resolved)
	return &cfg, nil
}
