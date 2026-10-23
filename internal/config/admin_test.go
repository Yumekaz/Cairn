package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdminCredentialCreationDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "admin.token")
	if err := CreateAdminToken(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || len(first) < 43 {
		t.Fatal("invalid credential permissions or entropy length")
	}
	if err := CreateAdminToken(path); err == nil {
		t.Fatal("existing credential overwritten")
	}
	again, err := os.ReadFile(path)
	if err != nil || string(again) != string(first) {
		t.Fatal("credential changed")
	}
	other := filepath.Join(t.TempDir(), "other.token")
	if err := CreateAdminToken(other); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(other)
	if err != nil || string(second) == string(first) {
		t.Fatal("credentials are not independently random")
	}
}
