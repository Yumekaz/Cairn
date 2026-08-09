package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var hostKey []byte

// InitHostKey initializes the host key from dataDir/host.key or creates it.
//
// A host key is mandatory. If it cannot be read or created, the caller must
// stop startup rather than allowing secrets to be encrypted with a shared
// fallback key.
func InitHostKey(dataDir string) error {
	hostKey = nil

	keyPath := filepath.Join(dataDir, "host.key")
	if dataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve host key directory: %w", err)
		}
		keyPath = filepath.Join(homeDir, ".cairn", "host.key")
	}

	// Try to read existing key
	content, err := os.ReadFile(keyPath)
	if err == nil {
		if len(content) != 32 {
			return fmt.Errorf("host key %s has invalid length %d; expected 32 bytes", keyPath, len(content))
		}
		if err := os.Chmod(keyPath, 0600); err != nil {
			return fmt.Errorf("secure host key permissions: %w", err)
		}
		hostKey = append([]byte(nil), content...)
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("read host key %s: %w", keyPath, err)
	}

	// Generate a new 32-byte key
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return fmt.Errorf("generate host key: %w", err)
	}

	keyDir := filepath.Dir(keyPath)
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("create host key directory: %w", err)
	}
	if err := os.Chmod(keyDir, 0700); err != nil {
		return fmt.Errorf("secure host key directory permissions: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create host key %s: %w", keyPath, err)
	}
	if _, err := keyFile.Write(newKey); err != nil {
		keyFile.Close()
		return fmt.Errorf("write host key %s: %w", keyPath, err)
	}
	if err := keyFile.Sync(); err != nil {
		keyFile.Close()
		return fmt.Errorf("sync host key %s: %w", keyPath, err)
	}
	if err := keyFile.Close(); err != nil {
		return fmt.Errorf("close host key %s: %w", keyPath, err)
	}

	hostKey = newKey
	return nil
}

func getEncryptionKey() ([]byte, error) {
	if len(hostKey) == 32 {
		return hostKey, nil
	}
	return nil, fmt.Errorf("host key is not initialized")
}

// EncryptSecret encrypts plaintext string using AES-GCM and base64 encodes the result.
func EncryptSecret(plaintext string) (string, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptSecret decrypts a base64 encoded ciphertext using AES-GCM.
func DecryptSecret(ciphertextStr string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextStr)
	if err != nil {
		return "", err
	}

	key, err := getEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
