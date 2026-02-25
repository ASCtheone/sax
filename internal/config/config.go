package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Theme struct {
	Bg              string `json:"bg"`
	Fg              string `json:"fg"`
	FgMuted         string `json:"fg_muted"`
	Accent          string `json:"accent"`
	AccentSecondary string `json:"accent_secondary"`
	Surface         string `json:"surface"`
	SurfaceDark     string `json:"surface_dark"`
	Success         string `json:"success"`
	BorderInactive  string `json:"border_inactive"`
}

func DefaultTheme() Theme {
	return Theme{
		Bg:              "#0a0e14",
		Fg:              "#c0e0ff",
		FgMuted:         "#5c7a99",
		Accent:          "#7dcfff",
		AccentSecondary: "#ff9e64",
		Surface:         "#1a1e2e",
		SurfaceDark:     "#0d1117",
		Success:         "#73daca",
		BorderInactive:  "#2a3040",
	}
}

// ThemePresets contains named theme palettes.
var ThemePresets = map[string]Theme{
	"neon-blue": {
		Bg:              "#0a0e14",
		Fg:              "#c0e0ff",
		FgMuted:         "#5c7a99",
		Accent:          "#7dcfff",
		AccentSecondary: "#ff9e64",
		Surface:         "#1a1e2e",
		SurfaceDark:     "#0d1117",
		Success:         "#73daca",
		BorderInactive:  "#2a3040",
	},
	"gruvbox": {
		Bg:              "#282828",
		Fg:              "#ebdbb2",
		FgMuted:         "#928374",
		Accent:          "#458588",
		AccentSecondary: "#d79921",
		Surface:         "#3c3836",
		SurfaceDark:     "#1d2021",
		Success:         "#689d6a",
		BorderInactive:  "#504945",
	},
	"catppuccin-mocha": {
		Bg:              "#1e1e2e",
		Fg:              "#cdd6f4",
		FgMuted:         "#6c7086",
		Accent:          "#89b4fa",
		AccentSecondary: "#fab387",
		Surface:         "#313244",
		SurfaceDark:     "#181825",
		Success:         "#a6e3a1",
		BorderInactive:  "#45475a",
	},
	"tokyo-night": {
		Bg:              "#1a1b26",
		Fg:              "#c0caf5",
		FgMuted:         "#565f89",
		Accent:          "#7aa2f7",
		AccentSecondary: "#ff9e64",
		Surface:         "#24283b",
		SurfaceDark:     "#16161e",
		Success:         "#9ece6a",
		BorderInactive:  "#3b4261",
	},
}

type Config struct {
	AutoUpdate    bool      `json:"auto_update"`
	LastCheckTime time.Time `json:"last_check_time"`
	LatestVersion string    `json:"latest_version"`
	ThemeName     string    `json:"theme_name"`
	Theme         Theme     `json:"theme"`
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

// ConfigPath returns the path to the config file (for display purposes).
func ConfigPath() string {
	return configPath()
}

// PresetNames returns the sorted list of available theme preset names.
func PresetNames() []string {
	names := make([]string, 0, len(ThemePresets))
	for name := range ThemePresets {
		names = append(names, name)
	}
	// Stable order
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

// Load reads the config from disk. Returns defaults if the file doesn't exist.
func Load() (*Config, error) {
	cfg := &Config{AutoUpdate: true}

	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Theme = DefaultTheme()
			_ = cfg.Save()
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, err
	}

	// Resolve theme: if theme_name matches a preset, use it as the base.
	// Only theme fields that differ from DefaultTheme() are treated as
	// intentional overrides (old configs have all defaults baked in).
	if cfg.ThemeName != "" {
		if preset, ok := ThemePresets[cfg.ThemeName]; ok {
			defaults := DefaultTheme()
			saved := cfg.Theme
			cfg.Theme = preset

			// Apply only intentional overrides (values that differ from defaults)
			if saved.Bg != "" && saved.Bg != defaults.Bg {
				cfg.Theme.Bg = saved.Bg
			}
			if saved.Fg != "" && saved.Fg != defaults.Fg {
				cfg.Theme.Fg = saved.Fg
			}
			if saved.FgMuted != "" && saved.FgMuted != defaults.FgMuted {
				cfg.Theme.FgMuted = saved.FgMuted
			}
			if saved.Accent != "" && saved.Accent != defaults.Accent {
				cfg.Theme.Accent = saved.Accent
			}
			if saved.AccentSecondary != "" && saved.AccentSecondary != defaults.AccentSecondary {
				cfg.Theme.AccentSecondary = saved.AccentSecondary
			}
			if saved.Surface != "" && saved.Surface != defaults.Surface {
				cfg.Theme.Surface = saved.Surface
			}
			if saved.SurfaceDark != "" && saved.SurfaceDark != defaults.SurfaceDark {
				cfg.Theme.SurfaceDark = saved.SurfaceDark
			}
			if saved.Success != "" && saved.Success != defaults.Success {
				cfg.Theme.Success = saved.Success
			}
			if saved.BorderInactive != "" && saved.BorderInactive != defaults.BorderInactive {
				cfg.Theme.BorderInactive = saved.BorderInactive
			}
			return cfg, nil
		}
	}

	// No preset — fill missing theme fields with defaults
	defaults := DefaultTheme()
	if cfg.Theme.Bg == "" {
		cfg.Theme.Bg = defaults.Bg
	}
	if cfg.Theme.Fg == "" {
		cfg.Theme.Fg = defaults.Fg
	}
	if cfg.Theme.FgMuted == "" {
		cfg.Theme.FgMuted = defaults.FgMuted
	}
	if cfg.Theme.Accent == "" {
		cfg.Theme.Accent = defaults.Accent
	}
	if cfg.Theme.AccentSecondary == "" {
		cfg.Theme.AccentSecondary = defaults.AccentSecondary
	}
	if cfg.Theme.Surface == "" {
		cfg.Theme.Surface = defaults.Surface
	}
	if cfg.Theme.SurfaceDark == "" {
		cfg.Theme.SurfaceDark = defaults.SurfaceDark
	}
	if cfg.Theme.Success == "" {
		cfg.Theme.Success = defaults.Success
	}
	if cfg.Theme.BorderInactive == "" {
		cfg.Theme.BorderInactive = defaults.BorderInactive
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
