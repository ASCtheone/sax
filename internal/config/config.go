package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Config struct {
	AutoUpdate    bool      `json:"auto_update"`
	LastCheckTime time.Time `json:"last_check_time"`
	LatestVersion string    `json:"latest_version"`
}

func configDir() string {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appData, "sax")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".sax")
	}
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

// Load reads the config from disk. Returns defaults if the file doesn't exist.
func Load() (*Config, error) {
	cfg := &Config{AutoUpdate: true}

	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			// Write defaults
			_ = cfg.Save()
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Save writes the config to disk with indentation.
func (c *Config) Save() error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0600)
}
