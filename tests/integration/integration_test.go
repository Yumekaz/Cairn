package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/cli"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/daemon"
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

func TestWorkersOneOffAndCron(t *testing.T) {
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

	// 1. Test Worker Workload
	t.Log("Deploying a worker service...")
	workerCfg := &api.ServiceConfig{
		Name:    "test-worker",
		Kind:    "worker",
		Image:   "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command: []string{"/bin/busybox", "sleep", "3600"},
	}

	var workerSvc api.Service
	err = client.Post(ctx, "/services", workerCfg, &workerSvc)
	if err != nil {
		t.Fatalf("failed to deploy worker service: %v", err)
	}
	defer client.Delete(ctx, "/services/test-worker", nil)

	if workerSvc.Route != "N/A" {
		t.Errorf("expected worker route to be 'N/A', got '%s'", workerSvc.Route)
	}

	dbPath := filepath.Join(homeDir, ".cairn", "cairn.db")
	st, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// 2. Test One-off Command
	t.Log("Testing one-off task container execution...")
	runReq := map[string]string{
		"command": "echo 'Hello from One-Off Task'",
	}

	socketClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}

	bodyBytes, _ := json.Marshal(runReq)
	resp, err := socketClient.Post("http://localhost/services/test-worker/run", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("failed to execute one-off run request: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	respStr := string(respBytes)
	t.Logf("One-off run output:\n%s", respStr)

	if !strings.Contains(respStr, "Hello from One-Off Task") {
		t.Errorf("expected logs to contain 'Hello from One-Off Task', got '%s'", respStr)
	}
	if !strings.Contains(respStr, "[cairn-exit-code] 0") {
		t.Errorf("expected logs to contain exit code marker '[cairn-exit-code] 0', got '%s'", respStr)
	}

	// Check that a job run was recorded in the database
	runs, err := st.ListJobRunsByService(workerSvc.ID)
	if err != nil {
		t.Fatalf("failed to list job runs: %v", err)
	}
	if len(runs) == 0 {
		t.Error("expected a job run history record to be created, but found none")
	} else {
		run := runs[0]
		if run.Type != "one-off" {
			t.Errorf("expected job run type 'one-off', got '%s'", run.Type)
		}
		if run.Status != "success" {
			t.Errorf("expected job run status 'success', got '%s'", run.Status)
		}
		if run.ExitCode == nil || *run.ExitCode != 0 {
			t.Errorf("expected exit code 0, got %v", run.ExitCode)
		}
	}

	// 3. Test Cron Scheduling
	t.Log("Deploying a cron service...")
	cronCfg := &api.ServiceConfig{
		Name:     "test-cron-service",
		Kind:     "cron",
		Image:    "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Schedule: "* * * * *",
		Run:      "echo 'Hello from Cron Job'",
	}

	var cronSvc api.Service
	err = client.Post(ctx, "/services", cronCfg, &cronSvc)
	if err != nil {
		t.Fatalf("failed to deploy cron service: %v", err)
	}
	defer client.Delete(ctx, "/services/test-cron-service", nil)

	// Verify no running containers are created at deployment time
	if cronSvc.RuntimeID != "" {
		t.Errorf("expected cron service RuntimeID to be empty, got '%s'", cronSvc.RuntimeID)
	}

	// Verify cron job is registered in SQLite
	cj, err := st.GetCronJobByName("test-cron-service")
	if err != nil {
		t.Fatalf("failed to get cron job: %v", err)
	}
	if cj == nil {
		t.Fatal("expected cron job 'test-cron-service' to exist in store, but found nil")
	}
	if cj.Schedule != "* * * * *" {
		t.Errorf("expected schedule '* * * * *', got '%s'", cj.Schedule)
	}

	// Test matches for June 22, 2026 (Monday) at 02:05:00
	t.Log("Verifying Cron Parser matches wildcard schedules correctly...")
	sched, err := daemon.ParseCron("*/5 2,3 * 6 1-5")
	if err != nil {
		t.Fatalf("failed to parse cron: %v", err)
	}

	testTime := time.Date(2026, 6, 22, 2, 5, 0, 0, time.UTC)
	if !sched.Matches(testTime) {
		t.Error("expected schedule to match test time (June 22, 2026 02:05:00)")
	}
}

func TestDatabaseServiceDumpAndRestore(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/home/yumekaz"
	}
	socketPath := filepath.Join(homeDir, ".cairn", "cairnd.sock")

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("Cairn daemon socket does not exist. Skipping integration tests.")
	}

	// 1. Mock pg_dump and psql scripts inside rootfs
	pgDumpPath := "/home/yumekaz/Desktop/Mini-Docker/rootfs/bin/pg_dump"
	psqlPath := "/home/yumekaz/Desktop/Mini-Docker/rootfs/bin/psql"

	pgDumpScript := `#!/bin/sh
echo "INSERT INTO test VALUES ('recovered-row');" > /backup_vol/backup_dump.sql
`
	psqlScript := `#!/bin/sh
cat /backup_vol/restore_dump.sql > /backup_vol/restored_rows.txt
`
	if err := os.WriteFile(pgDumpPath, []byte(pgDumpScript), 0755); err != nil {
		t.Fatalf("failed to write mock pg_dump: %v", err)
	}
	defer os.Remove(pgDumpPath)

	if err := os.WriteFile(psqlPath, []byte(psqlScript), 0755); err != nil {
		t.Fatalf("failed to write mock psql: %v", err)
	}
	defer os.Remove(psqlPath)

	client := cli.NewDaemonClient(socketPath)
	ctx := context.Background()

	// Clean up any existing volume folder for integration-db-vol
	volPath := filepath.Join(homeDir, ".cairn", "volumes", "integration-db-vol")
	os.RemoveAll(volPath)

	// 2. Deploy database service
	postgresCfg := &api.ServiceConfig{
		Name:      "integration-db",
		Kind:      "postgres",
		Image:     "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command:   []string{"/bin/busybox", "sleep", "3600"},
		Volumes: []api.VolumeConfig{
			{Name: "integration-db-vol", MountPath: "/backup_vol"},
		},
		Environment: map[string]string{
			"POSTGRES_USER":     "myuser",
			"POSTGRES_PASSWORD": "mypassword",
			"POSTGRES_DB":       "mydb",
		},
	}

	t.Log("Deploying integration-db service...")
	var dbSvc api.Service
	err = client.Post(ctx, "/services", postgresCfg, &dbSvc)
	if err != nil {
		t.Fatalf("failed to deploy database service: %v", err)
	}
	defer client.Delete(ctx, "/services/integration-db", nil)

	// Assert route is N/A
	if dbSvc.Route != "N/A" {
		t.Errorf("expected database service route to be 'N/A', got '%s'", dbSvc.Route)
	}

	// 3. Deploy client service (uses dynamic IP resolution)
	clientCfg := &api.ServiceConfig{
		Name:      "client-service",
		Kind:      "web",
		Image:     "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command:   []string{"/bin/busybox", "sleep", "3600"},
		Environment: map[string]string{
			"DATABASE_URL": "postgres://myuser:mypassword@integration-db:5432/mydb",
		},
	}

	t.Log("Deploying client-service...")
	var clientSvc api.Service
	err = client.Post(ctx, "/services", clientCfg, &clientSvc)
	if err != nil {
		t.Fatalf("failed to deploy client service: %v", err)
	}
	defer client.Delete(ctx, "/services/client-service", nil)

	// 4. Verify resolved environment variables
	dbInfoPath := filepath.Join("/var/lib/mini-docker/containers", dbSvc.RuntimeID, "config.json")
	dbInfoBytes, err := os.ReadFile(dbInfoPath)
	if err != nil {
		t.Fatalf("failed to read db container config: %v", err)
	}
	var dbConfig struct {
		Network struct {
			IP string `json:"ip"`
		} `json:"network"`
	}
	if err := json.Unmarshal(dbInfoBytes, &dbConfig); err != nil {
		t.Fatalf("failed to parse db container config: %v", err)
	}
	dbIP := dbConfig.Network.IP
	t.Logf("Database service IP is: %s", dbIP)

	clientInfoPath := filepath.Join("/var/lib/mini-docker/containers", clientSvc.RuntimeID, "config.json")
	clientInfoBytes, err := os.ReadFile(clientInfoPath)
	if err != nil {
		t.Fatalf("failed to read client container config: %v", err)
	}
	var clientConfig struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(clientInfoBytes, &clientConfig); err != nil {
		t.Fatalf("failed to parse client container config: %v", err)
	}

	resolvedURL := clientConfig.Env["DATABASE_URL"]
	t.Logf("Resolved DATABASE_URL: %s", resolvedURL)
	expectedURL := fmt.Sprintf("postgres://myuser:mypassword@%s:5432/mydb", dbIP)
	if resolvedURL != expectedURL {
		t.Errorf("expected resolved DATABASE_URL to be '%s', got '%s'", expectedURL, resolvedURL)
	}

	// 5. Write some simulated data to volume path
	testFileHostPath := filepath.Join(volPath, "test_rows.txt")
	if err := os.MkdirAll(volPath, 0755); err != nil {
		t.Fatalf("failed to create volume path: %v", err)
	}
	initialData := "recovered-row"
	if err := os.WriteFile(testFileHostPath, []byte(initialData), 0644); err != nil {
		t.Fatalf("failed to write test data: %v", err)
	}

	// 6. Trigger logical backup
	t.Log("Triggering logical database dump backup...")
	var backup api.Backup
	backupCreatePath := "/volumes/integration-db-vol/backups"
	err = client.Post(ctx, backupCreatePath, nil, &backup)
	if err != nil {
		t.Fatalf("failed to trigger backup: %v", err)
	}
	t.Logf("Logical backup created: %s (Status: %s)", backup.ID, backup.Status)
	if backup.Status != "success" {
		t.Fatalf("expected backup status 'success', got '%s'", backup.Status)
	}

	// 7. Corrupt test database rows (delete the file)
	t.Log("Corrupting test database rows...")
	os.Remove(testFileHostPath)

	// 8. Trigger logical restore
	t.Log("Triggering logical database restore...")
	restoreReq := map[string]string{
		"backup_id": backup.ID,
	}
	var restoreResp struct {
		Status string `json:"status"`
	}
	restorePath := "/volumes/integration-db-vol/restore"
	err = client.Post(ctx, restorePath, restoreReq, &restoreResp)
	if err != nil {
		t.Fatalf("failed to trigger restore: %v", err)
	}
	t.Logf("Restore completed: %s", restoreResp.Status)

	// 9. Verify restored data
	restoredFileHostPath := filepath.Join(volPath, "restored_rows.txt")
	restoredBytes, err := os.ReadFile(restoredFileHostPath)
	if err != nil {
		t.Fatalf("failed to read restored rows file: %v", err)
	}
	restoredData := string(restoredBytes)
	t.Logf("Restored rows content:\n%s", restoredData)
	if !strings.Contains(restoredData, "recovered-row") {
		t.Errorf("expected restored rows to contain '%s', got '%s'", initialData, restoredData)
	}
}

func TestDashboardV1(t *testing.T) {
	// 1. Create a temporary data directory and store for test
	tempDir, err := os.MkdirTemp("", "cairn-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "cairn.db")
	st, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// 2. Setup a daemon config with a random free port for DashboardAddr
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on random port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close() // Close it so the daemon can bind to it

	socketPath := filepath.Join(tempDir, "cairnd.sock")
	cfg := &config.DaemonConfig{
		SocketPath:    socketPath,
		DatabasePath:  dbPath,
		DataDir:       tempDir,
		VolumeDir:     filepath.Join(tempDir, "volumes"),
		BackupDir:     filepath.Join(tempDir, "backups"),
		DashboardAddr: addr,
	}

	// 3. Create server
	// We can pass nil or mock for RuntimeBackend since we are only testing routes and static asset serving
	srv := daemon.NewServer(cfg, st, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Start(ctx); err != nil && err != http.ErrServerClosed {
			t.Logf("server start error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(200 * time.Millisecond)

	// 4. Verify we can fetch the dashboard assets from the TCP address
	clientHttp := &http.Client{Timeout: 2 * time.Second}
	
	// Test redirect / -> /dashboard/
	resp, err := clientHttp.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("failed to reach root redirect: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 (after redirect), got %d", resp.StatusCode)
	}

	// Test index.html
	resp, err = clientHttp.Get("http://" + addr + "/dashboard/index.html")
	if err != nil {
		t.Fatalf("failed to fetch index.html: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("failed to read index.html body: %v", err)
	}
	if !strings.Contains(string(body), "<title>Cairn Dashboard</title>") {
		t.Errorf("index.html body does not contain expected title. Body: %s", string(body))
	}

	// Test index.css
	resp, err = clientHttp.Get("http://" + addr + "/dashboard/index.css")
	if err != nil {
		t.Fatalf("failed to fetch index.css: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for index.css, got %d", resp.StatusCode)
	}

	// Test index.js
	resp, err = clientHttp.Get("http://" + addr + "/dashboard/index.js")
	if err != nil {
		t.Fatalf("failed to fetch index.js: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for index.js, got %d", resp.StatusCode)
	}

	// 5. Test GET /services/{name}/deploys on a dummy service
	// Create a dummy service in the store
	dummySvc := &api.Service{
		ID:           "test-service-id",
		Name:         "test-service",
		Kind:         "web",
		DesiredState: "running",
		ActualState:  "running",
	}
	if err := st.UpsertService(dummySvc); err != nil {
		t.Fatalf("failed to create dummy service: %v", err)
	}

	// Create a dummy deploy record
	dummyDeploy := &api.Deploy{
		ID:        "test-deploy-id",
		ServiceID: "test-service-id",
		Version:   "1.0.0",
		Status:    "success",
		CreatedAt: time.Now(),
	}
	if err := st.CreateDeploy(dummyDeploy); err != nil {
		t.Fatalf("failed to create dummy deploy: %v", err)
	}

	resp, err = clientHttp.Get("http://" + addr + "/services/test-service/deploys")
	if err != nil {
		t.Fatalf("failed to fetch deploys API: %v", err)
	}
	var deploys []*api.Deploy
	if err := json.NewDecoder(resp.Body).Decode(&deploys); err != nil {
		resp.Body.Close()
		t.Fatalf("failed to decode deploys response: %v", err)
	}
	resp.Body.Close()

	if len(deploys) != 1 || deploys[0].ID != "test-deploy-id" {
		t.Errorf("expected 1 deploy with ID 'test-deploy-id', got: %v", deploys)
	}
}


