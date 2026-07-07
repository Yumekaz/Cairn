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

var defaultKey = []byte("cairn-paas-default-32bytes-crypt") // 32 bytes default key
var hostKey []byte

// InitHostKey initializes the host key from dataDir/host.key or creates it.
func InitHostKey(dataDir string) {
	keyPath := filepath.Join(dataDir, "host.key")
	if dataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			keyPath = filepath.Join(homeDir, ".cairn", "host.key")
		}
	}

	// Try to read existing key
	if content, err := os.ReadFile(keyPath); err == nil && len(content) == 32 {
		hostKey = content
		return
	}

	// Generate a new 32-byte key
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err == nil {
		_ = os.MkdirAll(filepath.Dir(keyPath), 0700)
		if err := os.WriteFile(keyPath, newKey, 0600); err == nil {
			hostKey = newKey
			return
		}
	}

	// Fallback
	hostKey = defaultKey
}

func getEncryptionKey() []byte {
	if len(hostKey) == 32 {
		return hostKey
	}
	return defaultKey
}

// EncryptSecret encrypts plaintext string using AES-GCM and base64 encodes the result.
func EncryptSecret(plaintext string) (string, error) {
	key := getEncryptionKey()
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

	key := getEncryptionKey()
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
