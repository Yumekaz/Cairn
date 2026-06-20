package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("expected non-nil default config")
	}
	if cfg.SocketPath == "" {
		t.Error("expected default SocketPath to be non-empty")
	}
	if cfg.DatabasePath == "" {
		t.Error("expected default DatabasePath to be non-empty")
	}
	if cfg.DataDir == "" {
		t.Error("expected default DataDir to be non-empty")
	}
}

func TestParseServiceConfig(t *testing.T) {
	yamlContent := `
name: test-app
kind: web
image: /path/to/rootfs
command: ["/bin/sh", "-c", "echo hello"]
migration: "python manage.py db upgrade"
ports:
  - host: 9090
    container: 80
volumes:
  - name: test-vol
    mount_path: /mnt/test
healthcheck:
  http_path: /healthz
  interval: 5s
  timeout: 2s
  retries: 3
  startup_grace: 10s
restart:
  policy: on-failure
  max_retries: 5
`
	tmpFile, err := os.CreateTemp("", "cairn-service-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(yamlContent)); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	svcCfg, err := ParseServiceConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("ParseServiceConfig failed: %v", err)
	}

	if svcCfg.Name != "test-app" {
		t.Errorf("expected name 'test-app', got '%s'", svcCfg.Name)
	}
	if svcCfg.Kind != "web" {
		t.Errorf("expected kind 'web', got '%s'", svcCfg.Kind)
	}
	if svcCfg.Image != "/path/to/rootfs" {
		t.Errorf("expected image '/path/to/rootfs', got '%s'", svcCfg.Image)
	}
	if len(svcCfg.Command) != 3 || svcCfg.Command[0] != "/bin/sh" {
		t.Errorf("unexpected command: %v", svcCfg.Command)
	}
	if svcCfg.Migration != "python manage.py db upgrade" {
		t.Errorf("expected migration 'python manage.py db upgrade', got '%s'", svcCfg.Migration)
	}
	if len(svcCfg.Ports) != 1 || svcCfg.Ports[0].Host != 9090 || svcCfg.Ports[0].Container != 80 {
		t.Errorf("unexpected ports mapping: %v", svcCfg.Ports)
	}
	if len(svcCfg.Volumes) != 1 || svcCfg.Volumes[0].Name != "test-vol" || svcCfg.Volumes[0].MountPath != "/mnt/test" {
		t.Errorf("unexpected volumes config: %v", svcCfg.Volumes)
	}
	if svcCfg.HealthCheck == nil || svcCfg.HealthCheck.HTTPPath != "/healthz" || svcCfg.HealthCheck.Interval != 5*time.Second {
		t.Errorf("unexpected healthcheck config: %v", svcCfg.HealthCheck)
	}
	if svcCfg.Restart == nil || svcCfg.Restart.Policy != "on-failure" || svcCfg.Restart.MaxRetries != 5 {
		t.Errorf("unexpected restart config: %v", svcCfg.Restart)
	}
}

func TestParseServiceConfigInvalid(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cairn-service-invalid-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write invalid yaml
	if _, err := tmpFile.Write([]byte("invalid_yaml: : :")); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	_, err = ParseServiceConfig(tmpFile.Name())
	if err == nil {
		t.Error("expected parsing of invalid YAML to fail, but it succeeded")
	}
}

func TestLoadSaveDaemonConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cairn-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "cairnd-config.yaml")
	cfg := &DaemonConfig{
		SocketPath:       "/tmp/test-cairnd.sock",
		DatabasePath:     "/tmp/test-cairn.db",
		DataDir:          "/tmp/test-cairn",
		VolumeDir:        "/tmp/test-volumes",
		BackupDir:        "/tmp/test-backups",
		MiniDockerSocket: "/tmp/test-minidocker.sock",
	}

	err = SaveDaemonConfig(cfg, configPath)
	if err != nil {
		t.Fatalf("SaveDaemonConfig failed: %v", err)
	}

	loaded, err := LoadDaemonConfig(configPath)
	if err != nil {
		t.Fatalf("LoadDaemonConfig failed: %v", err)
	}

	if loaded.SocketPath != cfg.SocketPath {
		t.Errorf("expected SocketPath '%s', got '%s'", cfg.SocketPath, loaded.SocketPath)
	}
	if loaded.DatabasePath != cfg.DatabasePath {
		t.Errorf("expected DatabasePath '%s', got '%s'", cfg.DatabasePath, loaded.DatabasePath)
	}
	if loaded.VolumeDir != cfg.VolumeDir {
		t.Errorf("expected VolumeDir '%s', got '%s'", cfg.VolumeDir, loaded.VolumeDir)
	}
}
