package daemon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTarGz builds an in-memory tar.gz with the given entries.
func buildTarGz(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range entries {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	path := filepath.Join(t.TempDir(), "test.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func TestExtractTarGzAcceptsNormalEntries(t *testing.T) {
	src := buildTarGz(t, map[string]string{
		"data/redis/dump.rdb": "payload",
	})
	dest := t.TempDir()
	if err := ExtractTarGz(src, dest); err != nil {
		t.Fatalf("ExtractTarGz failed on benign archive: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(dest, "data", "redis", "dump.rdb"))
	if err != nil || string(out) != "payload" {
		t.Fatalf("extracted file missing or wrong: out=%q err=%v", out, err)
	}
}

func TestExtractTarGzRejectsPathTraversal(t *testing.T) {
	payload := "pwned"
	parent := t.TempDir()
	for _, name := range []string{
		"../../escape.txt",
		"a/../../../escape2.txt",
	} {
		src := buildTarGz(t, map[string]string{name: payload})
		dest := filepath.Join(parent, "dest")
		err := ExtractTarGz(src, dest)
		if err == nil {
			t.Fatalf("expected error for entry %q, got nil", name)
		}
		if !strings.Contains(err.Error(), "illegal path") {
			t.Fatalf("expected zip-slip rejection for %q, got: %v", name, err)
		}
	}
	// Nothing should have been written outside dest by the rejected attempts
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(parent), "escape*.txt"))
	if len(matches) != 0 {
		t.Fatalf("path traversal escaped destination: %v", matches)
	}
	matches, _ = filepath.Glob(filepath.Join(parent, "escape*.txt"))
	if len(matches) != 0 {
		t.Fatalf("path traversal escaped destination: %v", matches)
	}
}
