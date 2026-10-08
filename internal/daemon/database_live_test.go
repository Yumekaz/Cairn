package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yumekaz/cairn/internal/api"
	"github.com/yumekaz/cairn/internal/config"
	"github.com/yumekaz/cairn/internal/duraflow"
	"github.com/yumekaz/cairn/internal/runtime"
	"github.com/yumekaz/cairn/internal/store"
)

// This adapter tests Cairn's production logical dump/restore functions with
// real database/client binaries. Docker supplies isolated task containers;
// Mini-Docker namespace/mount behavior has a separate privileged proof suite.
type liveDatabaseTasks struct {
	runtime.RuntimeBackend
	database string
	volume   string
}

func liveDocker(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *liveDatabaseTasks) CreateContainer(ctx context.Context, cfg *api.ServiceConfig, name string) (string, error) {
	args := []string{"create", "--network", "container:" + r.database, "--memory", "128m", "--pids-limit", "64", "--user", cfg.User,
		"--mount", "type=bind,src=" + r.volume + ",dst=/backup_vol"}
	var keys []string
	for key := range cfg.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--env", key+"="+cfg.Environment[key])
	}
	args = append(args, "--entrypoint", cfg.Command[0], cfg.Image)
	args = append(args, cfg.Command[1:]...)
	return liveDocker(ctx, args...)
}

func (r *liveDatabaseTasks) StartContainer(ctx context.Context, id string) error {
	_, err := liveDocker(ctx, "start", id)
	return err
}
func (r *liveDatabaseTasks) StopContainer(ctx context.Context, id string) error {
	_, err := liveDocker(ctx, "stop", "--time", "5", id)
	return err
}
func (r *liveDatabaseTasks) RemoveContainer(ctx context.Context, id string) error {
	if id == r.database {
		return fmt.Errorf("refusing to remove the database through task cleanup")
	}
	_, err := liveDocker(ctx, "rm", "-f", id)
	return err
}
func (r *liveDatabaseTasks) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	out, err := liveDocker(ctx, "inspect", id)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		State struct {
			Running  bool
			ExitCode int
		}
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, err
	}
	state := runtime.StateStopped
	if rows[0].State.Running {
		state = runtime.StateRunning
	}
	code := rows[0].State.ExitCode
	return &runtime.ContainerInfo{ID: id, State: state, IPAddress: "127.0.0.1", ExitCode: &code}, nil
}
func (r *liveDatabaseTasks) StreamLogs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error) {
	out, err := liveDocker(ctx, "logs", id)
	return io.NopCloser(strings.NewReader(out)), err
}

func TestLivePostgresBackupRestore(t *testing.T) {
	database := os.Getenv("CAIRN_TEST_POSTGRES_CONTAINER")
	if image := os.Getenv("CAIRN_TEST_POSTGRES_IMAGE"); image != "" {
		_, _, service, _ := newLiveDatabaseFixture(t, "postgres", image)
		database = service.RuntimeID
		liveDatabaseReady(t, database, "pg_isready", "-U", "postgres")
	}
	if database == "" {
		t.Skip("set CAIRN_TEST_POSTGRES_CONTAINER to a disposable PostgreSQL container")
	}
	ctx := context.Background()
	image, err := liveDocker(ctx, "inspect", "--format", "{{.Image}}", database)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("database image: %s", image)
	dbname := "cairn_proof_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sql := func(db, statement string) string {
		t.Helper()
		out, err := liveDocker(ctx, "exec", database, "psql", "-U", "postgres", "-d", db, "-v", "ON_ERROR_STOP=1", "-At", "-c", statement)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	sql("postgres", "CREATE DATABASE "+dbname)
	t.Cleanup(func() { _, _ = liveDocker(ctx, "exec", database, "dropdb", "-U", "postgres", "--force", dbname) })
	sql(dbname, "CREATE TABLE records(id integer primary key, value text); INSERT INTO records VALUES(1,'snapshot')")
	dir := t.TempDir()
	// The Docker snap has a private /tmp mount; use a visible host directory
	// for the task bind mount rather than the test process's private /tmp.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	volume, err := os.MkdirTemp(home, "cairn-db-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(volume) })
	cfg := &api.ServiceConfig{Name: "live-pg", Kind: "postgres", Image: image,
		Environment: map[string]string{"POSTGRES_USER": "postgres", "POSTGRES_DB": dbname}}
	service := &api.Service{Name: cfg.Name, Kind: cfg.Kind, RuntimeID: database, CurrentDeployID: "proof"}
	vol := &api.Volume{Name: "pg-data", HostPath: volume}
	tasks := &liveDatabaseTasks{database: database, volume: volume}
	server := &Server{config: &config.DaemonConfig{DataDir: dir}, runtime: tasks}
	cfgPath := filepath.Join(dir, "services", cfg.Name, "deploy_proof.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	backup := &api.Backup{ID: uuid.NewString(), BackupPath: filepath.Join(dir, "snapshot.sql.gz")}
	checksum, size, err := server.performPostgresDumpBackup(vol, service, cfg, backup.BackupPath, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checksum == "" || size == 0 {
		t.Fatal("empty logical backup")
	}
	backup.Checksum, backup.SizeBytes = checksum, size
	sql(dbname, "UPDATE records SET value='changed'; INSERT INTO records VALUES(2,'after-snapshot')")
	if err := server.performPostgresRestore(ctx, vol, service, backup); err != nil {
		t.Fatal(err)
	}
	if got := sql(dbname, "SELECT id || ':' || value FROM records ORDER BY id"); got != "1:snapshot" {
		t.Fatalf("restored rows = %q, want selected snapshot only", got)
	}

	// A valid gzip containing bad SQL must fail the restore and roll back any
	// preceding statement, rather than reporting success after partial changes.
	badSQL := filepath.Join(dir, "bad.sql")
	if err := os.WriteFile(badSQL, []byte("UPDATE records SET value='partial'; SELECT * FROM no_such_table;"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := &api.Backup{ID: uuid.NewString(), BackupPath: filepath.Join(dir, "bad.sql.gz")}
	if _, _, err := CompressFileToGzip(badSQL, bad.BackupPath); err != nil {
		t.Fatal(err)
	}
	if err := server.performPostgresRestore(ctx, vol, service, bad); err == nil {
		t.Fatal("invalid SQL restore was reported successful")
	}
	if got := sql(dbname, "SELECT value FROM records WHERE id=1"); got != "snapshot" {
		t.Fatalf("failed restore changed data: %s", got)
	}
}

func newLiveDatabaseFixture(t *testing.T, kind, image string) (*Server, *api.Volume, *api.Service, *api.ServiceConfig) {
	t.Helper()
	ctx := context.Background()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	volume, err := os.MkdirTemp(home, "cairn-db-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(volume) })
	name := "cairn-proof-" + uuid.NewString()
	memoryLimit := "512m"
	if kind == "mongodb" {
		memoryLimit = "768m"
	}
	args := []string{"run", "-d", "--name", name, "--network", "none", "--memory", memoryLimit, "--cpus", "1", "--pids-limit", "128"}
	if kind == "postgres" {
		args = append(args, "--tmpfs", "/var/lib/postgresql:rw,size=134217728", "--env", "POSTGRES_HOST_AUTH_METHOD=trust", image)
	} else if kind == "redis" {
		args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--mount", "type=bind,src="+volume+",dst=/data",
			image, "redis-server", "--save", "", "--appendonly", "no", "--dir", "/data")
	} else {
		args = append(args, "--tmpfs", "/data/db:rw,size=536870912", image, "mongod", "--bind_ip", "127.0.0.1", "--wiredTigerCacheSizeGB", "0.25")
	}
	if _, err := liveDocker(ctx, args...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := liveDocker(ctx, "logs", "--tail", "60", name)
			t.Logf("database failure logs: %s", logs)
		}
		_, _ = liveDocker(ctx, "rm", "-f", name)
	})
	imageID, err := liveDocker(ctx, "inspect", "--format", "{{.Image}}", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s database image: %s", kind, imageID)
	dir := t.TempDir()
	st, err := store.NewStore(filepath.Join(dir, "cairn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &api.ServiceConfig{Name: name, Kind: kind, Image: imageID}
	service := &api.Service{ID: uuid.NewString(), Name: name, Kind: kind, RuntimeID: name, CurrentDeployID: "proof", DesiredState: "running", ActualState: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	vol := &api.Volume{ID: uuid.NewString(), Name: name + "-data", HostPath: volume, AttachedServiceID: service.ID, MountPath: "/data", Status: "available", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.UpsertService(service); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVolume(vol); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "services", name, "deploy_proof.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return &Server{store: st, config: &config.DaemonConfig{DataDir: dir}, runtime: &liveDatabaseTasks{database: name, volume: volume}}, vol, service, cfg
}

func liveDatabaseReady(t *testing.T, database string, command ...string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		_, last = liveDocker(context.Background(), append([]string{"exec", database}, command...)...)
		if last == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal(last)
}

func liveRestoreSteps(server *Server, vol *api.Volume, backup *api.Backup) error {
	input, _ := json.Marshal(RestoreInput{VolumeName: vol.Name, BackupID: backup.ID})
	ctx := &duraflow.StepContext{Context: context.Background(), InputJSON: string(input), State: make(map[string]string)}
	for _, step := range []duraflow.StepExecutor{server.execRestoreStopContainer, server.execRestoreVerifyAndExtract, server.execRestoreStartContainer} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

func TestLiveRedisBackupRestore(t *testing.T) {
	image := os.Getenv("CAIRN_TEST_REDIS_IMAGE")
	if image == "" {
		t.Skip("set CAIRN_TEST_REDIS_IMAGE to enable a disposable real Redis proof")
	}
	server, vol, service, cfg := newLiveDatabaseFixture(t, "redis", image)
	liveDatabaseReady(t, service.RuntimeID, "redis-cli", "PING")
	redis := func(args ...string) string {
		t.Helper()
		out, err := liveDocker(context.Background(), append([]string{"exec", service.RuntimeID, "redis-cli", "--raw"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	redis("SET", "record", "snapshot")
	backup := &api.Backup{ID: uuid.NewString(), VolumeID: vol.ID, BackupPath: filepath.Join(server.config.DataDir, "snapshot.rdb.gz"), Status: "success", CreatedAt: time.Now()}
	var err error
	backup.Checksum, backup.SizeBytes, err = server.performRedisDumpBackup(vol, service, cfg, backup.BackupPath, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.CreateBackup(backup); err != nil {
		t.Fatal(err)
	}
	redis("SET", "record", "changed")
	redis("SET", "later", "after-snapshot")
	if err := liveRestoreSteps(server, vol, backup); err != nil {
		t.Fatal(err)
	}
	liveDatabaseReady(t, service.RuntimeID, "redis-cli", "PING")
	if got := redis("GET", "record"); got != "snapshot" {
		t.Fatalf("restored record=%q", got)
	}
	if got := redis("EXISTS", "later"); got != "0" {
		t.Fatalf("post-snapshot key survived: %s", got)
	}
	bad := *backup
	bad.ID = uuid.NewString()
	bad.Checksum = "invalid"
	if err := server.store.CreateBackup(&bad); err != nil {
		t.Fatal(err)
	}
	if err := liveRestoreSteps(server, vol, &bad); err == nil {
		t.Fatal("corrupted backup accepted")
	}
	liveDatabaseReady(t, service.RuntimeID, "redis-cli", "PING")
	if got := redis("GET", "record"); got != "snapshot" {
		t.Fatalf("checksum failure changed data: %s", got)
	}
}

func TestLiveMongoBackupRestore(t *testing.T) {
	image := os.Getenv("CAIRN_TEST_MONGO_IMAGE")
	if image == "" {
		t.Skip("set CAIRN_TEST_MONGO_IMAGE to enable a disposable real MongoDB proof")
	}
	server, vol, service, cfg := newLiveDatabaseFixture(t, "mongodb", image)
	liveDatabaseReady(t, service.RuntimeID, "mongosh", "--quiet", "--eval", "quit(db.runCommand({ping:1}).ok ? 0 : 1)")
	mongo := func(statement string) string {
		t.Helper()
		out, err := liveDocker(context.Background(), "exec", service.RuntimeID, "mongosh", "--quiet", "proof", "--eval", statement)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	mongo(`db.records.insertOne({_id:1,value:"snapshot"})`)
	backup := &api.Backup{ID: uuid.NewString(), VolumeID: vol.ID, BackupPath: filepath.Join(server.config.DataDir, "snapshot.archive.gz"), Status: "success", CreatedAt: time.Now()}
	var err error
	backup.Checksum, backup.SizeBytes, err = server.performMongoDumpBackup(vol, service, cfg, backup.BackupPath, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.CreateBackup(backup); err != nil {
		t.Fatal(err)
	}
	mongo(`db.records.updateOne({_id:1},{$set:{value:"changed"}});db.records.insertOne({_id:2,value:"after-snapshot"})`)
	if err := liveRestoreSteps(server, vol, backup); err != nil {
		t.Fatal(err)
	}
	if got := mongo(`print(JSON.stringify(db.records.find().sort({_id:1}).toArray()))`); got != `[{"_id":1,"value":"snapshot"}]` {
		t.Fatalf("restored documents=%s", got)
	}
	bad := *backup
	bad.ID = uuid.NewString()
	bad.Checksum = "invalid"
	if err := server.store.CreateBackup(&bad); err != nil {
		t.Fatal(err)
	}
	if err := liveRestoreSteps(server, vol, &bad); err == nil {
		t.Fatal("corrupted backup accepted")
	}
	if got := mongo(`print(db.records.findOne({_id:1}).value)`); got != "snapshot" {
		t.Fatalf("checksum failure changed data: %s", got)
	}
}
