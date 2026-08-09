package daemon

import (
	"reflect"
	"testing"
)

func TestMongoCommandsUseStructuredArguments(t *testing.T) {
	maliciousIP := "db.internal; touch /tmp/pwned"
	maliciousUser := "admin$(touch /tmp/user)"
	maliciousPassword := "p@ss; echo compromised"

	dump := mongoDumpCommand(maliciousIP, maliciousUser, maliciousPassword)
	wantDump := []string{
		"mongodump",
		"--host=" + maliciousIP,
		"--username=" + maliciousUser,
		"--password=" + maliciousPassword,
		"--authenticationDatabase=admin",
		"--archive=/backup_vol/backup_dump.archive",
	}
	if !reflect.DeepEqual(dump, wantDump) {
		t.Fatalf("mongo dump command = %#v, want %#v", dump, wantDump)
	}

	restore := mongoRestoreCommand(maliciousIP, maliciousUser, maliciousPassword)
	wantRestore := []string{
		"mongorestore",
		"--host=" + maliciousIP,
		"--username=" + maliciousUser,
		"--password=" + maliciousPassword,
		"--authenticationDatabase=admin",
		"--drop",
		"--archive=/backup_vol/restore_dump.archive",
	}
	if !reflect.DeepEqual(restore, wantRestore) {
		t.Fatalf("mongo restore command = %#v, want %#v", restore, wantRestore)
	}

	for _, command := range [][]string{dump, restore} {
		for _, arg := range command {
			if arg == "sh" || arg == "-c" {
				t.Fatalf("Mongo command unexpectedly invokes a shell: %#v", command)
			}
		}
	}
}

func TestMongoCommandsOmitIncompleteAuthentication(t *testing.T) {
	want := []string{"mongodump", "--host=db.internal", "--archive=/backup_vol/backup_dump.archive"}
	if got := mongoDumpCommand("db.internal", "admin", ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("mongo dump command = %#v, want %#v", got, want)
	}
}

func TestDatabaseBackupCommandsUseLiteralArguments(t *testing.T) {
	if got := postgresDumpCommand("10.0.0.1;touch /tmp/pwned", "user name", "db"); !reflect.DeepEqual(got, []string{
		"pg_dump", "-h", "10.0.0.1;touch /tmp/pwned", "-U", "user name", "-d", "db", "-f", "/backup_vol/backup_dump.sql",
	}) {
		t.Fatalf("unexpected postgres dump argv: %#v", got)
	}
	if got := postgresRestoreCommand("10.0.0.1$(id)", "user", "db;drop"); !reflect.DeepEqual(got, []string{
		"psql", "-h", "10.0.0.1$(id)", "-U", "user", "-d", "db;drop", "-f", "/backup_vol/restore_dump.sql",
	}) {
		t.Fatalf("unexpected postgres restore argv: %#v", got)
	}
	if got := redisDumpCommand("10.0.0.1 && touch /tmp/pwned"); !reflect.DeepEqual(got, []string{
		"redis-cli", "-h", "10.0.0.1 && touch /tmp/pwned", "--rdb", "/backup_vol/backup_dump.rdb",
	}) {
		t.Fatalf("unexpected redis dump argv: %#v", got)
	}
}
