package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var (
	current Settings
	mu      sync.RWMutex
)

func Load(path string) error {
	data, err := os.ReadFile(path)

	if err != nil {
		if os.IsNotExist(err) {
			def := Default()

			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}

			raw, _ := json.MarshalIndent(def, "", "  ")
			if err := os.WriteFile(path, raw, 0644); err != nil {
				return err
			}

			current = def
			return nil
		}
		return err
	}

	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	mu.Lock()
	current = s
	mu.Unlock()
	return nil
}

func Get() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func Set(s Settings) {
	mu.Lock()
	current = s
	mu.Unlock()
}
