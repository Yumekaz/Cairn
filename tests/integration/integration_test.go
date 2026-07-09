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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/cli"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/daemon"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)
func TestEndToEndMLP(t *testing.T) {
	cleanStaleIptables(t, 8080, 8081, 8082, 8085)
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
	for i := 0; i < 10; i++ {
		resp, err = clientHttp.Get(routeUrl)
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
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
	runDB := func(fn func(st *store.Store) error) error {
		dbPath := filepath.Join(homeDir, ".cairn", "cairn.db")
		st, err := store.NewStore(dbPath)
		if err != nil {
			return err
		}
		defer st.Close()
		return fn(st)
	}

	// Verify that StateTouched is true for Deploy 1 in SQLite database
	var d1 *api.Deploy
	err = runDB(func(st *store.Store) error {
		var e error
		d1, e = st.GetDeploy(deployID1)
		return e
	})
	if err != nil {
		t.Fatalf("failed to get deploy 1: %v", err)
	}
	if !d1.StateTouched {
		t.Error("expected deploy 1 to have StateTouched = true")
	}

	// Verify that a volume backup was automatically created for migration-vol
	var vol *api.Volume
	err = runDB(func(st *store.Store) error {
		var e error
		vol, e = st.GetVolumeByName("migration-vol")
		return e
	})
	if err != nil {
		t.Fatalf("failed to get volume: %v", err)
	}
	if vol == nil {
		t.Fatal("expected volume migration-vol to exist")
	}

	var backups []*api.Backup
	err = runDB(func(st *store.Store) error {
		var e error
		backups, e = st.ListBackups(vol.ID)
		return e
	})
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

	var d2 *api.Deploy
	err = runDB(func(st *store.Store) error {
		var e error
		d2, e = st.GetDeploy(deployID2)
		return e
	})
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

	var d3 *api.Deploy
	err = runDB(func(st *store.Store) error {
		var e error
		d3, e = st.GetDeploy(deployID3)
		return e
	})
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
	var runs []*api.JobRun
	{
		st, err := store.NewStore(dbPath)
		if err != nil {
			t.Fatalf("failed to open store: %v", err)
		}
		runs, err = st.ListJobRunsByService(workerSvc.ID)
		st.Close()
		if err != nil {
			t.Fatalf("failed to list job runs: %v", err)
		}
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
	var cj *api.CronJob
	{
		st, err := store.NewStore(dbPath)
		if err != nil {
			t.Fatalf("failed to open store: %v", err)
		}
		cj, err = st.GetCronJobByName("test-cron-service")
		st.Close()
		if err != nil {
			t.Fatalf("failed to get cron job: %v", err)
		}
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

type fakeRuntime struct {
	mu         sync.Mutex
	containers map[string]*runtime.ContainerInfo
	onStart    func(id string)
	blockStart bool
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		containers: make(map[string]*runtime.ContainerInfo),
	}
}

func (f *fakeRuntime) CreateContainer(ctx context.Context, cfg *api.ServiceConfig, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := name + "-id"
	f.containers[id] = &runtime.ContainerInfo{
		ID:        id,
		Name:      name,
		Image:     cfg.Image,
		State:     runtime.StateCreated,
		IPAddress: "127.0.0.1",
	}
	return id, nil
}

func (f *fakeRuntime) StartContainer(ctx context.Context, id string) error {
	f.mu.Lock()
	if c, ok := f.containers[id]; ok {
		c.State = runtime.StateRunning
	}
	f.mu.Unlock()

	if f.onStart != nil {
		f.onStart(id)
	}

	if f.blockStart {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeRuntime) StopContainer(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.containers[id]; ok {
		c.State = runtime.StateStopped
	}
	return nil
}

func (f *fakeRuntime) RestartContainer(ctx context.Context, id string) error {
	return nil
}

func (f *fakeRuntime) RemoveContainer(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.containers, id)
	return nil
}

func (f *fakeRuntime) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.containers[id]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("container not found: %s", id)
}

func (f *fakeRuntime) StreamLogs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeRuntime) ListContainers(ctx context.Context) ([]*runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var list []*runtime.ContainerInfo
	for _, c := range f.containers {
		list = append(list, c)
	}
	return list, nil
}

func TestDuraFlowCrashRecovery(t *testing.T) {
	// 1. Create a temporary data directory and store for test
	tempDir, err := os.MkdirTemp("", "cairn-test-crash-*")
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

	// Setup a service record
	svc := &api.Service{
		ID:           "test-crash-svc-id",
		Name:         "test-crash-svc",
		Kind:         "web",
		DesiredState: "running",
		ActualState:  "stopped",
	}
	if err := st.UpsertService(svc); err != nil {
		t.Fatalf("failed to insert service: %v", err)
	}

	// Setup deploy record
	deployID := "test-crash-deploy-id"
	deploy := &api.Deploy{
		ID:        deployID,
		ServiceID: svc.ID,
		Version:   "1.0.0",
		Status:    "pending",
		Stage:     "starting",
	}
	if err := st.CreateDeploy(deploy); err != nil {
		t.Fatalf("failed to create deploy: %v", err)
	}

	// Prepare service config
	cfg := &api.ServiceConfig{
		Name:  "test-crash-svc",
		Kind:  "web",
		Image: "nginx:latest",
		Ports: []api.PortMapping{
			{Host: 8081, Container: 80},
		},
	}

	// Save deploy config on disk
	svcDir := filepath.Join(tempDir, "services", cfg.Name)
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatalf("failed to create service dir: %v", err)
	}
	cfgJSON, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(svcDir, fmt.Sprintf("deploy_%s.json", deployID))
	if err := os.WriteFile(cfgPath, cfgJSON, 0644); err != nil {
		t.Fatalf("failed to write deploy config: %v", err)
	}

	// 2. Setup fake runtime
	runtimeFake := newFakeRuntime()
	runtimeFake.blockStart = true
	
	// Create context to control daemon lifecycle
	ctx1, cancel1 := context.WithCancel(context.Background())
	
	// Channel to signal when to crash/cancel the first daemon instance
	startedChan := make(chan bool, 1)

	// Set onStart handler to cancel the daemon's context when container is started
	runtimeFake.onStart = func(id string) {
		t.Logf("Fake runtime started container: %s. Crashing daemon...", id)
		startedChan <- true
		cancel1()
	}

	// Setup daemon config with a random free port for DashboardAddr
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on random port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	daemonCfg := &config.DaemonConfig{
		SocketPath:    filepath.Join(tempDir, "cairnd.sock"),
		DatabasePath:  dbPath,
		DataDir:       tempDir,
		VolumeDir:     filepath.Join(tempDir, "volumes"),
		BackupDir:     filepath.Join(tempDir, "backups"),
		DashboardAddr: addr,
	}

	srv1 := daemon.NewServer(daemonCfg, st, runtimeFake)
	
	// Start the first server in a goroutine
	go func() {
		_ = srv1.Start(ctx1)
	}()

	// Wait for server to initialize
	time.Sleep(200 * time.Millisecond)

	// Trigger deployment by calling POST /services
	clientHttp := &http.Client{Timeout: 5 * time.Second}
	postData, _ := json.Marshal(cfg)
	
	t.Log("Triggering initial deployment workflow...")
	go func() {
		resp, err := clientHttp.Post("http://"+addr+"/services", "application/json", bytes.NewBuffer(postData))
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait for container start to signal, causing cancel1()
	select {
	case <-startedChan:
		t.Log("Interrupted deploy workflow successfully. Waiting for daemon 1 to stop...")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for deployment workflow to start candidate container")
	}

	// Give daemon 1 a moment to shut down
	time.Sleep(300 * time.Millisecond)

	// Verify that workflow status in the database is still "running" (not failed/success)
	// Query workflows
	wfs, err := st.ListWorkflows()
	if err != nil {
		t.Fatalf("failed to list workflows: %v", err)
	}
	if len(wfs) == 0 {
		t.Fatal("expected at least one workflow to be registered")
	}
	wf := wfs[0]
	t.Logf("Interrupted workflow status: %s, CurrentStepIndex: %d", wf.Status, wf.CurrentStepIndex)
	if wf.Status != "running" {
		t.Errorf("expected workflow status to be 'running', got '%s'", wf.Status)
	}

	// 3. Start a second daemon instance in-process to reconcile and complete deployment
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// In the second daemon, we change the fake runtime's start container behavior so it doesn't block,
	// allowing the resumed workflow to complete.
	runtimeFake2 := newFakeRuntime()
	runtimeFake2.blockStart = false
	// Re-add container info so the inspect works on resumed run
	runtimeFake2.CreateContainer(context.Background(), cfg, fmt.Sprintf("cairn-test-crash-svc-%s", wf.ID[:8]))

	srv2 := daemon.NewServer(daemonCfg, st, runtimeFake2)
	
	t.Log("Starting daemon 2 to perform reconciliation...")
	go func() {
		_ = srv2.Start(ctx2)
	}()

	// Poll the database to verify the workflow is picked up, resumed, and completed successfully
	t.Log("Polling workflow completion status...")
	completed := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		wfUpdated, err := st.GetWorkflow(wf.ID)
		if err == nil && wfUpdated != nil {
			t.Logf("Polled workflow status: %s, step index: %d", wfUpdated.Status, wfUpdated.CurrentStepIndex)
			if wfUpdated.Status == "success" {
				completed = true
				break
			}
			if wfUpdated.Status == "failed" {
				t.Fatalf("Resumed workflow failed: %v", wfUpdated)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !completed {
		t.Fatal("Timeout waiting for resumed workflow to complete successfully")
	}

	t.Log("Workflow completed successfully after reconciliation! Verifying final service state...")
	updatedSvc, err := st.GetService(svc.ID)
	if err != nil {
		t.Fatalf("failed to retrieve service: %v", err)
	}
	if updatedSvc.ActualState != "running" {
		t.Errorf("expected service actual state 'running', got '%s'", updatedSvc.ActualState)
	}
}

func TestReliabilityHardening(t *testing.T) {
	// 1. Create a temporary data directory and store for test
	tempDir, err := os.MkdirTemp("", "cairn-test-reliability-*")
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

	// 2. Setup fake runtime
	runtimeFake := newFakeRuntime()

	// 3. Initialize daemon config
	daemonCfg := &config.DaemonConfig{
		SocketPath:   filepath.Join(tempDir, "cairnd.sock"),
		DatabasePath: dbPath,
		DataDir:      tempDir,
		VolumeDir:    filepath.Join(tempDir, "volumes"),
		BackupDir:    filepath.Join(tempDir, "backups"),
	}

	srv := daemon.NewServer(daemonCfg, st, runtimeFake)
	ctx := context.Background()

	// --- TEST CASE 1: Host Reboot / Container Recreation Recovery ---
	t.Run("RebootRecovery", func(t *testing.T) {
		// Setup a service record with desired state = running and current deploy ID
		deployID := "deploy-reboot-id-12345"
		svc := &api.Service{
			ID:              "svc-reboot-id",
			Name:            "svc-reboot",
			Kind:            "web",
			CurrentDeployID: deployID,
			DesiredState:    "running",
			ActualState:     "stopped",
		}
		if err := st.UpsertService(svc); err != nil {
			t.Fatalf("failed to upsert service: %v", err)
		}

		// Prepare and save the deploy config file
		cfg := &api.ServiceConfig{
			Name:  "svc-reboot",
			Kind:  "web",
			Image: "nginx:alpine",
			Ports: []api.PortMapping{
				{Host: 8085, Container: 80},
			},
		}
		svcDir := filepath.Join(tempDir, "services", cfg.Name)
		if err := os.MkdirAll(svcDir, 0755); err != nil {
			t.Fatalf("failed to create service dir: %v", err)
		}
		cfgJSON, _ := json.Marshal(cfg)
		cfgPath := filepath.Join(svcDir, fmt.Sprintf("deploy_%s.json", deployID))
		if err := os.WriteFile(cfgPath, cfgJSON, 0644); err != nil {
			t.Fatalf("failed to write deploy config: %v", err)
		}

		// Reconcile immediately: should create and start the container
		srv.ReconcileServices(ctx)

		// Assert that the container exists in fake runtime and has StateRunning
		updatedSvc, err := st.GetService(svc.ID)
		if err != nil {
			t.Fatalf("failed to get service: %v", err)
		}
		if updatedSvc.RuntimeID == "" {
			t.Fatalf("expected service to have a runtime ID assigned after reconciliation")
		}

		c, err := runtimeFake.InspectContainer(ctx, updatedSvc.RuntimeID)
		if err != nil {
			t.Fatalf("failed to inspect container: %v", err)
		}
		if c.State != runtime.StateRunning {
			t.Errorf("expected container to be running, got: %s", c.State)
		}
		if updatedSvc.ActualState != "running" {
			t.Errorf("expected service actual state to be running, got: %s", updatedSvc.ActualState)
		}

		// Now simulate container stopped (e.g. host rebooted, container stopped but not removed)
		runtimeFake.StopContainer(ctx, updatedSvc.RuntimeID)

		// Reconcile: should restart it
		srv.ReconcileServices(ctx)

		c, err = runtimeFake.InspectContainer(ctx, updatedSvc.RuntimeID)
		if err != nil {
			t.Fatalf("failed to inspect container: %v", err)
		}
		if c.State != runtime.StateRunning {
			t.Errorf("expected container to be restarted/running, got: %s", c.State)
		}
	})

	// --- TEST CASE 2: Dangling Container Cleanup ---
	t.Run("DanglingContainerCleanup", func(t *testing.T) {
		// Create a container directly in fake runtime starting with "cairn-"
		danglingID := "cairn-dangling-123-id"
		runtimeFake.mu.Lock()
		runtimeFake.containers[danglingID] = &runtime.ContainerInfo{
			ID:        danglingID,
			Name:      "cairn-dangling-123",
			Image:     "nginx:alpine",
			State:     runtime.StateRunning,
			IPAddress: "127.0.0.1",
		}
		runtimeFake.mu.Unlock()

		// Run reconciliation
		srv.ReconcileServices(ctx)

		// Verify container was removed
		runtimeFake.mu.Lock()
		_, exists := runtimeFake.containers[danglingID]
		runtimeFake.mu.Unlock()

		if exists {
			t.Errorf("expected dangling container to be removed by reconciliation")
		}
	})

	// --- TEST CASE 3: Metadata Backup and Pruning ---
	t.Run("MetadataBackup", func(t *testing.T) {
		// Verify BackupMetadata works
		err := srv.BackupMetadata()
		if err != nil {
			t.Fatalf("BackupMetadata failed: %v", err)
		}

		backupDir := filepath.Join(tempDir, "backups", "metadata")
		files, err := os.ReadDir(backupDir)
		if err != nil {
			t.Fatalf("failed to read backup dir: %v", err)
		}
		if len(files) != 1 {
			t.Errorf("expected exactly 1 backup, got %d", len(files))
		}

		// Create 6 dummy backups with different timestamps to test pruning (keeping 5 most recent + the one just made)
		for _, f := range files {
			_ = os.Remove(filepath.Join(backupDir, f.Name()))
		}

		// Write 6 dummy backup files
		for i := 1; i <= 6; i++ {
			dummyName := fmt.Sprintf("cairn_20260621_12000%d.db", i)
			dummyPath := filepath.Join(backupDir, dummyName)
			if err := os.WriteFile(dummyPath, []byte("dummy-db-content"), 0644); err != nil {
				t.Fatalf("failed to write dummy backup: %v", err)
			}
		}

		// Run BackupMetadata again: it will write a new one (timestamp format matches time.Now())
		// and prune the oldest ones, keeping exactly 5 most recent in total.
		err = srv.BackupMetadata()
		if err != nil {
			t.Fatalf("BackupMetadata failed: %v", err)
		}

		files, err = os.ReadDir(backupDir)
		if err != nil {
			t.Fatalf("failed to read backup dir: %v", err)
		}

		// Total should be exactly 5
		if len(files) != 5 {
			t.Errorf("expected exactly 5 backups after pruning, got %d", len(files))
		}

		// Verify the oldest ones were removed
		for _, f := range files {
			if f.Name() == "cairn_20260621_120001.db" || f.Name() == "cairn_20260621_120002.db" {
				t.Errorf("backup file %s should have been pruned", f.Name())
			}
		}
	})
}

func TestReverseProxyAndEnvs(t *testing.T) {
	// 1. Check if socket exists
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/home/yumekaz"
	}
	socketPath := filepath.Join(homeDir, ".cairn", "cairnd.sock")
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("Cairn socket does not exist. Skipping proxy/envs integration test.")
	}

	client := cli.NewDaemonClient(socketPath)
	ctx := context.Background()

	// Load daemon config to get DashboardAddr
	cfg, err := config.LoadDaemonConfig("")
	if err != nil {
		t.Fatalf("failed to load daemon config: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	projectRoot := wd
	if strings.HasSuffix(projectRoot, "tests/integration") {
		projectRoot = filepath.Dir(filepath.Dir(projectRoot))
	}
	cairnYamlPath := filepath.Join(projectRoot, "examples", "counter-api", "cairn.yaml")

	// 2. Deploy valid service config
	svcCfg, err := config.ParseServiceConfig(cairnYamlPath)
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	t.Log("Deploying service for proxy/envs testing...")
	var svc api.Service
	err = client.Post(ctx, "/services", svcCfg, &svc)
	if err != nil {
		t.Fatalf("failed to deploy: %v", err)
	}
	defer client.Delete(ctx, "/services/counter-api", nil)

	// 3. Test HTTP reverse proxy to container using Host header on DashboardAddr
	clientHttp := &http.Client{Timeout: 3 * time.Second}
	reqUrl := fmt.Sprintf("http://%s/index.html", cfg.DashboardAddr)
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "counter-api.localhost"

	t.Logf("Verifying reverse proxy routing via Host header '%s' to '%s'...", req.Host, reqUrl)
	resp, err := clientHttp.Do(req)
	if err != nil {
		t.Fatalf("failed to reach service through reverse proxy: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 via proxy, got %d. Body: %s", resp.StatusCode, string(bodyBytes))
	}

	// 4. Test environment variables (Set, List, Masking, Delete)
	t.Log("Setting a custom environment variable...")
	var setRes map[string]string
	setReq := map[string]interface{}{
		"key":       "TEST_CUSTOM_ENV",
		"value":     "cairn-val-123",
		"is_secret": false,
	}
	err = client.Post(ctx, "/services/counter-api/env", setReq, &setRes)
	if err != nil {
		t.Fatalf("failed to set env var: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Log("Setting a service secret...")
	secretReq := map[string]interface{}{
		"key":       "TEST_SECRET_ENV",
		"value":     "my-super-secret-key",
		"is_secret": true,
	}
	err = client.Post(ctx, "/services/counter-api/env", secretReq, &setRes)
	if err != nil {
		t.Fatalf("failed to set secret: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Log("Listing custom env vars and verifying masking...")
	var envs []*api.ServiceEnvVar
	err = client.Get(ctx, "/services/counter-api/env", &envs)
	if err != nil {
		t.Fatalf("failed to list env vars: %v", err)
	}

	foundEnv := false
	foundSecret := false
	for _, env := range envs {
		if env.Key == "TEST_CUSTOM_ENV" {
			foundEnv = true
			if env.Value != "cairn-val-123" {
				t.Errorf("expected TEST_CUSTOM_ENV value 'cairn-val-123', got '%s'", env.Value)
			}
		}
		if env.Key == "TEST_SECRET_ENV" {
			foundSecret = true
			if env.Value != "[REDACTED]" {
				t.Errorf("expected secret TEST_SECRET_ENV to be masked as '[REDACTED]', got '%s'", env.Value)
			}
		}
	}

	if !foundEnv {
		t.Error("TEST_CUSTOM_ENV not found in listed env vars")
	}
	if !foundSecret {
		t.Error("TEST_SECRET_ENV not found in listed env vars")
	}

	time.Sleep(3 * time.Second)

	t.Log("Deleting custom environment variable...")
	var delRes map[string]string
	err = client.Delete(ctx, "/services/counter-api/env/TEST_CUSTOM_ENV", &delRes)
	if err != nil {
		t.Fatalf("failed to delete env var: %v", err)
	}

	time.Sleep(3 * time.Second)

	// Verify it was deleted
	err = client.Get(ctx, "/services/counter-api/env", &envs)
	if err == nil {
		for _, env := range envs {
			if env.Key == "TEST_CUSTOM_ENV" {
				t.Error("TEST_CUSTOM_ENV should have been deleted")
			}
		}
	}

	time.Sleep(1 * time.Second)

	// 5. Stop service and verify 503 Service Unavailable page
	t.Log("Stopping service to verify 503 routing...")
	var stopRes api.Service
	err = client.Post(ctx, "/services/counter-api/stop", nil, &stopRes)
	if err != nil {
		t.Fatalf("failed to stop service: %v", err)
	}

	t.Log("Verifying 503 page response...")
	resp, err = clientHttp.Do(req)
	if err != nil {
		t.Fatalf("failed to reach stopped service: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 Service Unavailable, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(bodyBytes), "Service Unavailable") {
		t.Errorf("response body should contain 'Service Unavailable', got: %s", string(bodyBytes))
	}
}

func TestRedisAndMongoBackupRestore(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/home/yumekaz"
	}
	socketPath := filepath.Join(homeDir, ".cairn", "cairnd.sock")

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("Cairn daemon socket does not exist. Skipping integration tests.")
	}

	// 1. Mock redis-cli, mongodump and mongorestore inside rootfs
	redisCliPath := "/home/yumekaz/Desktop/Mini-Docker/rootfs/bin/redis-cli"
	mongoDumpPath := "/home/yumekaz/Desktop/Mini-Docker/rootfs/bin/mongodump"
	mongoRestorePath := "/home/yumekaz/Desktop/Mini-Docker/rootfs/bin/mongorestore"

	redisCliScript := `#!/bin/sh
echo "REDIS-DUMMY-DATA" > /backup_vol/backup_dump.rdb
`
	mongoDumpScript := `#!/bin/sh
echo "MONGO-DUMMY-DATA" > /backup_vol/backup_dump.archive
`
	mongoRestoreScript := `#!/bin/sh
cat /backup_vol/restore_dump.archive > /backup_vol/restored_mongo.txt
`

	if err := os.WriteFile(redisCliPath, []byte(redisCliScript), 0755); err != nil {
		t.Fatalf("failed to write mock redis-cli: %v", err)
	}
	defer os.Remove(redisCliPath)

	if err := os.WriteFile(mongoDumpPath, []byte(mongoDumpScript), 0755); err != nil {
		t.Fatalf("failed to write mock mongodump: %v", err)
	}
	defer os.Remove(mongoDumpPath)

	if err := os.WriteFile(mongoRestorePath, []byte(mongoRestoreScript), 0755); err != nil {
		t.Fatalf("failed to write mock mongorestore: %v", err)
	}
	defer os.Remove(mongoRestorePath)

	client := cli.NewDaemonClient(socketPath)
	ctx := context.Background()

	// Clean up any existing folders
	redisVolPath := filepath.Join(homeDir, ".cairn", "volumes", "integration-redis-vol")
	mongoVolPath := filepath.Join(homeDir, ".cairn", "volumes", "integration-mongo-vol")
	os.RemoveAll(redisVolPath)
	os.RemoveAll(mongoVolPath)

	// --- TEST REDIS ---
	t.Log("Deploying Redis service...")
	redisCfg := &api.ServiceConfig{
		Name:    "integration-redis",
		Kind:    "redis",
		Image:   "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command: []string{"/bin/busybox", "sleep", "3600"},
		Volumes: []api.VolumeConfig{
			{Name: "integration-redis-vol", MountPath: "/backup_vol"},
		},
		Environment: map[string]string{
			"REDIS_PASSWORD": "redissecretpass",
		},
	}
	var redisSvc api.Service
	if err := client.Post(ctx, "/services", redisCfg, &redisSvc); err != nil {
		t.Fatalf("failed to deploy redis service: %v", err)
	}
	defer client.Delete(ctx, "/services/integration-redis", nil)

	time.Sleep(3 * time.Second)

	t.Log("Creating Redis volume backup...")
	var redisBackup api.Backup
	if err := client.Post(ctx, "/volumes/integration-redis-vol/backups", nil, &redisBackup); err != nil {
		t.Fatalf("failed to backup redis: %v", err)
	}

	time.Sleep(3 * time.Second)

	// Stop service before restore to simulate typical file-based restore behavior
	var stopRes api.Service
	if err := client.Post(ctx, "/services/integration-redis/stop", nil, &stopRes); err != nil {
		t.Fatalf("failed to stop redis service: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Log("Restoring Redis volume backup...")
	restoreReq := map[string]string{"backup_id": redisBackup.ID}
	var restoreRes map[string]string
	if err := client.Post(ctx, "/volumes/integration-redis-vol/restore", restoreReq, &restoreRes); err != nil {
		t.Fatalf("failed to restore redis backup: %v", err)
	}

	// Verify dump.rdb file is created inside the Redis volume directory
	dumpRdbPath := filepath.Join(redisVolPath, "dump.rdb")
	if _, err := os.Stat(dumpRdbPath); os.IsNotExist(err) {
		t.Error("expected dump.rdb to be decompressed in volume directory after restore, but it does not exist")
	}

	time.Sleep(3 * time.Second)

	// --- TEST MONGO ---
	t.Log("Deploying MongoDB service...")
	mongoCfg := &api.ServiceConfig{
		Name:    "integration-mongo",
		Kind:    "mongodb",
		Image:   "/home/yumekaz/Desktop/Mini-Docker/rootfs",
		Command: []string{"/bin/busybox", "sleep", "3600"},
		Volumes: []api.VolumeConfig{
			{Name: "integration-mongo-vol", MountPath: "/backup_vol"},
		},
		Environment: map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": "mongouser",
			"MONGO_INITDB_ROOT_PASSWORD": "mongopassword",
		},
	}
	var mongoSvc api.Service
	if err := client.Post(ctx, "/services", mongoCfg, &mongoSvc); err != nil {
		t.Fatalf("failed to deploy mongo service: %v", err)
	}
	defer client.Delete(ctx, "/services/integration-mongo", nil)

	time.Sleep(3 * time.Second)

	t.Log("Creating MongoDB volume backup...")
	var mongoBackup api.Backup
	if err := client.Post(ctx, "/volumes/integration-mongo-vol/backups", nil, &mongoBackup); err != nil {
		t.Fatalf("failed to backup mongo: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Log("Restoring MongoDB volume backup...")
	restoreReqMongo := map[string]string{"backup_id": mongoBackup.ID}
	if err := client.Post(ctx, "/volumes/integration-mongo-vol/restore", restoreReqMongo, &restoreRes); err != nil {
		t.Fatalf("failed to restore mongo backup: %v", err)
	}

	// Verify restored_mongo.txt is created and populated inside the Mongo volume directory
	mongoTxtPath := filepath.Join(mongoVolPath, "restored_mongo.txt")
	content, err := os.ReadFile(mongoTxtPath)
	if err != nil {
		t.Fatalf("restored_mongo.txt was not found: %v", err)
	}
	if !strings.Contains(string(content), "MONGO-DUMMY-DATA") {
		t.Errorf("expected restored_mongo.txt to contain 'MONGO-DUMMY-DATA', got '%s'", string(content))
	}
}

func cleanStaleIptables(t *testing.T, ports ...int) {
	cmd := exec.Command("sudo", "-S", "iptables", "-t", "nat", "-S")
	cmd.Stdin = strings.NewReader("mihir\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Logf("Warning: failed to list iptables rules: %v (stderr: %s)", err, stderr.String())
		return
	}

	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		for _, port := range ports {
			if strings.Contains(line, fmt.Sprintf("--dport %d", port)) {
				rule := strings.TrimPrefix(line, "-A ")
				parts := strings.Fields(rule)
				if len(parts) == 0 {
					continue
				}
				chain := parts[0]
				ruleArgs := parts[1:]

				args := append([]string{"-S", "iptables", "-t", "nat", "-D", chain}, ruleArgs...)
				delCmd := exec.Command("sudo", args...)
				delCmd.Stdin = strings.NewReader("mihir\n")
				var delStderr bytes.Buffer
				delCmd.Stderr = &delStderr
				if err := delCmd.Run(); err != nil {
					t.Logf("Warning: failed to delete iptables rule '%s': %v (stderr: %s)", line, err, delStderr.String())
				} else {
					t.Logf("Cleaned up stale iptables rule: %s", line)
				}
			}
		}
	}
}



