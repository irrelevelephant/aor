package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RemoteConfig describes a remote afl server.
type RemoteConfig struct {
	URL string `json:"url"`
}

// Config holds afl configuration.
type Config struct {
	Remotes       map[string]RemoteConfig `json:"remotes"`
	DefaultRemote string                  `json:"default_remote,omitempty"`
}

// Path returns the default config file path (~/.afl/config.json).
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".afl", "config.json"), nil
}

// Load reads the config from ~/.afl/config.json.
// Returns a zero-value Config if the file does not exist.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes the config to ~/.afl/config.json.
func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(p, data, 0o644)
}

// ResolveRemote returns the default remote, or nil if none is configured.
func (c Config) ResolveRemote() *RemoteConfig {
	if c.DefaultRemote == "" {
		return nil
	}
	if r, ok := c.Remotes[c.DefaultRemote]; ok {
		return &r
	}
	return nil
}
