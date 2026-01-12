package settings

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

const apiKeyFilePath = "/etc/vestri/api.key"

func EnsureAPIKey() (string, bool, error) {
	key := strings.TrimSpace(GetAPIKey())
	if key != "" {
		if err := writeAPIKeyFile(key); err != nil {
			return "", false, err
		}
		return key, false, nil
	}

	fileKey, err := readAPIKeyFile()
	if err == nil && fileKey != "" {
		SetAPIKey(fileKey)
		return fileKey, false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", false, err
	}

	newKey, err := generateAPIKey()
	if err != nil {
		return "", false, err
	}
	if err := writeAPIKeyFile(newKey); err != nil {
		return "", false, err
	}

	SetAPIKey(newKey)
	return newKey, true, nil
}

func UpdateAPIKey(key string) error {
	key = strings.TrimSpace(key)

	if key == "" {
		SetAPIKey("")
		if err := os.Remove(apiKeyFilePath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if err := writeAPIKeyFile(key); err != nil {
		return err
	}
	SetAPIKey(key)
	return nil
}

func readAPIKeyFile() (string, error) {
	data, err := os.ReadFile(apiKeyFilePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeAPIKeyFile(key string) error {
	if err := os.MkdirAll(filepath.Dir(apiKeyFilePath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(apiKeyFilePath, []byte(key+"\n"), 0600); err != nil {
		return err
	}
	return os.Chmod(apiKeyFilePath, 0600)
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
