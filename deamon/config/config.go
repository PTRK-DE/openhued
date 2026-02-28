// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	LightID             string `json:"light_id"`
	BrightnessIncrement int    `json:"brightness_increment"`
	StreamBrightness    bool   `json:"stream_brightness"`
	CommandDebounceMs   int    `json:"command_debounce_ms"`
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "openhued", "daemon.json"), nil
}

func Load(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Config{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.LightID == "" {
		return nil, fmt.Errorf("config: light_id is required")
	}
	if cfg.BrightnessIncrement <= 0 {
		cfg.BrightnessIncrement = 5
	}
	if cfg.CommandDebounceMs <= 0 {
		cfg.CommandDebounceMs = 500
	}

	return &cfg, nil
}
