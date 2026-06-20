package integration

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/cli"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/store"
)

func TestEndToEndMLP(t *testing.T) {
	// 1. Check if the cairnd daemon socket exists. If not, skip integration tests.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/home/yumekaz"
	}
	socketPath := filepath.Join(homeDir, ".cairn", "cairnd.sock")

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("Cairn daemon socket does not exist. Skipping integration tests.")
	}

	// 2. Initialize Daemon client
	client := cli.NewDaemonClient(socketPath)
	ctx := context.Background()

	// Locate counter-api config paths relative to SERVER directory
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Since wd in go test for tests/integration is the directory containing the test,
	// we need to resolve paths relative to the project root.
	projectRoot := wd
	if strings.HasSuffix(projectRoot, "tests/integration") {
		projectRoot = filepath.Dir(filepath.Dir(projectRoot))
	}

	cairnYamlPath := filepath.Join(projectRoot, "examples", "counter-api", "cairn.yaml")
	brokenYamlPath := filepath.Join(projectRoot, "examples", "counter-api", "cairn_broken.yaml")

	t.Logf("Project Root: %s", projectRoot)
	t.Logf("Using cairn.yaml: %s", cairnYamlPath)

	// Step 1: Deploy valid service configuration
	svcCfg, err := config.ParseServiceConfig(cairnYamlPath)
	if err != nil {
		t.Fatalf("failed to parse config %s: %v", cairnYamlPath, err)
	}

	t.Log("Deploying service...")
	var result api.Service
	err = client.Post(ctx, "/services", svcCfg, &result)
	if err != nil {
		t.Fatalf("failed to deploy service: %v", err)
	}

	if result.Name != "counter-api" {
		t.Errorf("expected name 'counter-api', got '%s'", result.Name)
	}
	t.Logf("Service 'counter-api' deployed successfully (ID: %s, Runtime ID: %s)", result.ID, result.RuntimeID)

	// Verify route is accessible
	clientHttp := &http.Client{Timeout: 2 * time.Second}
	routeUrl := "http://localhost:8080/index.html"
	resp, err := clientHttp.Get(routeUrl)
	if err != nil {
		t.Fatalf("failed to reach deployed service on localhost:8080: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Step 2: Test volume persistence (Write and Read)
	volumeDir := filepath.Join(homeDir, ".cairn", "volumes", "counter-data")
	testFilePath := filepath.Join(volumeDir, "index.html")

	t.Logf("Writing state A to volume path: %s", testFilePath)
	stateA := "Backup Test State A"
	if err := os.WriteFile(testFilePath, []byte(stateA), 0644); err != nil {
		t.Fatalf("failed to write state A to volume: %v", err)
	}

	// Verify the web server reads state A from the volume
	resp, err = clientHttp.Get(routeUrl)
	if err != nil {
		t.Fatalf("failed to get route: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(bodyBytes)) != stateA {
		t.Errorf("expected content '%s', got '%s'", stateA, string(bodyBytes))
	}

	// Step 3: Create backup snapshot
	t.Log("Creating volume backup...")
	var backupResult api.Backup
	// The route is POST /volumes/{name}/backups
	backupPath := "/volumes/counter-data/backups"
	err = client.Post(ctx, backupPath, nil, &backupResult)
	if err != nil {
		t.Fatalf("failed to create backup: %v", err)
	}

	if backupResult.Status != "success" || backupResult.ID == "" {
		t.Errorf("unexpected backup status: %v", backupResult)
	}
	t.Logf("Backup created successfully (ID: %s, Path: %s)", backupResult.ID, backupResult.BackupPath)

	// Step 4: Modify data (Write state B)
	t.Log("Modifying data to state B...")
	stateB := "Backup Test State B"
	if err := os.WriteFile(testFilePath, []byte(stateB), 0644); err != nil {
		t.Fatalf("failed to write state B: %v", err)
	}

	// Verify route returns state B
	resp, err = clientHttp.Get(routeUrl)
	if err != nil {
		t.Fatalf("failed to get route: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(bodyBytes)) != stateB {
		t.Errorf("expected content '%s', got '%s'", stateB, string(bodyBytes))
	}

	// Step 5: Restore backup
	t.Log("Restoring backup...")
	var restoreRes map[string]string
	restorePath := "/volumes/counter-data/restore"
	restoreReq := map[string]string{
		"backup_id": backupResult.ID,
	}
	err = client.Post(ctx, restorePath, restoreReq, &restoreRes)
	if err != nil {
		t.Fatalf("failed to restore volume: %v", err)
	}

	if restoreRes["status"] != "restored" {
		t.Errorf("unexpected restore response: %v", restoreRes)
	}

	// Verify data has been reverted back to state A
	resp, err = clientHttp.Get(routeUrl)
	if err != nil {
		t.Fatalf("failed to get route after restore: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(bodyBytes)) != stateA {
		t.Errorf("expected content '%s' after restore, got '%s'", stateA, string(bodyBytes))
	}
	t.Log("Data successfully reverted to state A!")

	// Step 6: Deploy broken configuration (failed-deploy protection)
	t.Log("Deploying broken configuration (expecting failure)...")
	brokenCfg, err := config.ParseServiceConfig(brokenYamlPath)
	if err != nil {
		t.Fatalf("failed to parse broken config %s: %v", brokenYamlPath, err)
	}

	err = client.Post(ctx, "/services", brokenCfg, nil)
	if err == nil {
		t.Error("expected broken deploy to return an error, but it succeeded")
	} else {
		t.Logf("Broken deploy failed correctly with error: %v", err)
	}

	// Verify the original healthy container is still serving traffic
	resp, err = clientHttp.Get(routeUrl)
	if err != nil {
		t.Fatalf("failed to contact original service after failed deploy: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(bodyBytes)) != stateA {
		t.Errorf("expected original healthy state '%s', got '%s'", stateA, string(bodyBytes))
	}
	t.Log("Failed deploy protection verified! Original service remains active and healthy.")
}

func TestMigrationsAndRollback(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/home/yumekaz"
	}
	socketPath := filepath.Join(homeDir, ".cairn", "cairnd.sock")

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("Cairn daemon socket does not exist. Skipping integration tests.")
	}

	client := cli.NewDaemonClient(socketPath)
	ctx := context.Background()

	// 1. Deploy service with migration
	svcCfg1 := &api.ServiceConfig{
		Name:      "migrated-service",
		Kind:      "web",
		Image:     "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command:   []string{"/bin/busybox", "httpd", "-f", "-p", "80", "-h", "/www"},
		Migration: "echo \"Migrated State\" > /www/index.html",
		Ports: []api.PortMapping{
			{Host: 8082, Container: 80},
		},
		Volumes: []api.VolumeConfig{
			{Name: "migration-vol", MountPath: "/www"},
		},
		HealthCheck: &api.HealthCheckConfig{
			HTTPPath:     "/index.html",
			Interval:     1 * time.Second,
			Timeout:      1 * time.Second,
			Retries:      3,
			StartupGrace: 1 * time.Second,
		},
	}

	// Make sure target volume path is clean
	volPath := filepath.Join(homeDir, ".cairn", "volumes", "migration-vol")
	os.RemoveAll(volPath)

	t.Log("Deploying migrated-service (Deploy 1)...")
	var result1 api.Service
	err = client.Post(ctx, "/services", svcCfg1, &result1)
	if err != nil {
		t.Fatalf("failed to deploy service with migration: %v", err)
	}

	deployID1 := result1.CurrentDeployID
	t.Logf("Deploy 1 Succeeded (ID: %s)", deployID1)

	// Verify that StateTouched is true for Deploy 1 in SQLite database
	dbPath := filepath.Join(homeDir, ".cairn", "cairn.db")
	st, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	d1, err := st.GetDeploy(deployID1)
	if err != nil {
		t.Fatalf("failed to get deploy 1: %v", err)
	}
	if !d1.StateTouched {
		t.Error("expected deploy 1 to have StateTouched = true")
	}

	// Verify that a volume backup was automatically created for migration-vol
	vol, err := st.GetVolumeByName("migration-vol")
	if err != nil {
		t.Fatalf("failed to get volume: %v", err)
	}
	if vol == nil {
		t.Fatal("expected volume migration-vol to exist")
	}

	backups, err := st.ListBackups(vol.ID)
	if err != nil {
		t.Fatalf("failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		t.Error("expected a pre-deploy backup to have been automatically created")
	}

	// 2. Deploy service without migration (Deploy 2)
	svcCfg2 := &api.ServiceConfig{
		Name:    "migrated-service",
		Kind:    "web",
		Image:   "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command: []string{"/bin/busybox", "httpd", "-f", "-p", "80", "-h", "/www"},
		Ports: []api.PortMapping{
			{Host: 8082, Container: 80},
		},
		Volumes: []api.VolumeConfig{
			{Name: "migration-vol", MountPath: "/www"},
		},
		HealthCheck: &api.HealthCheckConfig{
			HTTPPath:     "/index.html",
			Interval:     1 * time.Second,
			Timeout:      1 * time.Second,
			Retries:      3,
			StartupGrace: 1 * time.Second,
		},
	}

	t.Log("Deploying migrated-service without migration (Deploy 2)...")
	var result2 api.Service
	err = client.Post(ctx, "/services", svcCfg2, &result2)
	if err != nil {
		t.Fatalf("failed to deploy service without migration: %v", err)
	}

	deployID2 := result2.CurrentDeployID
	t.Logf("Deploy 2 Succeeded (ID: %s)", deployID2)

	d2, err := st.GetDeploy(deployID2)
	if err != nil {
		t.Fatalf("failed to get deploy 2: %v", err)
	}
	if d2.StateTouched {
		t.Error("expected deploy 2 to have StateTouched = false")
	}

	// 3. Deploy service with migration (Deploy 3)
	svcCfg3 := &api.ServiceConfig{
		Name:      "migrated-service",
		Kind:      "web",
		Image:     "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command:   []string{"/bin/busybox", "httpd", "-f", "-p", "80", "-h", "/www"},
		Migration: "echo \"Migrated State 2\" > /www/index.html",
		Ports: []api.PortMapping{
			{Host: 8082, Container: 80},
		},
		Volumes: []api.VolumeConfig{
			{Name: "migration-vol", MountPath: "/www"},
		},
		HealthCheck: &api.HealthCheckConfig{
			HTTPPath:     "/index.html",
			Interval:     1 * time.Second,
			Timeout:      1 * time.Second,
			Retries:      3,
			StartupGrace: 1 * time.Second,
		},
	}

	t.Log("Deploying migrated-service with migration again (Deploy 3)...")
	var result3 api.Service
	err = client.Post(ctx, "/services", svcCfg3, &result3)
	if err != nil {
		t.Fatalf("failed to deploy service with migration 3: %v", err)
	}

	deployID3 := result3.CurrentDeployID
	t.Logf("Deploy 3 Succeeded (ID: %s)", deployID3)

	d3, err := st.GetDeploy(deployID3)
	if err != nil {
		t.Fatalf("failed to get deploy 3: %v", err)
	}
	if !d3.StateTouched {
		t.Error("expected deploy 3 to have StateTouched = true")
	}

	// 4. Try to rollback to Deploy 2 (without force)
	t.Log("Attempting unsafe rollback to Deploy 2 (expecting conflict)...")
	rollbackPath := "/services/migrated-service/rollback"
	rollbackReq := map[string]interface{}{
		"deploy_id": deployID2,
		"force":     false,
	}
	var rollbackRes api.Service
	err = client.Post(ctx, rollbackPath, rollbackReq, &rollbackRes)
	if err == nil {
		t.Error("expected unsafe rollback to fail with 409 Conflict, but it succeeded")
	} else {
		if !strings.Contains(err.Error(), "status 409") {
			t.Errorf("expected 409 Conflict error, got: %v", err)
		} else {
			t.Logf("Unsafe rollback correctly blocked: %v", err)
		}
	}

	// 5. Rollback to Deploy 2 with force = true
	t.Log("Attempting forced rollback to Deploy 2...")
	rollbackReq["force"] = true
	err = client.Post(ctx, rollbackPath, rollbackReq, &rollbackRes)
	if err != nil {
		t.Fatalf("forced rollback failed: %v", err)
	}

	t.Logf("Forced rollback succeeded! Current Deploy ID: %s", rollbackRes.CurrentDeployID)

	// Clean up service
	client.Delete(ctx, "/services/migrated-service", nil)
}
