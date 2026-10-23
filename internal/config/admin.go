package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// CreateAdminToken creates a high-entropy credential without printing it or
// overwriting an existing credential. Its parent must be operator-controlled.
func CreateAdminToken(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create admin credential: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(base64.RawURLEncoding.EncodeToString(secret) + "\n"); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
