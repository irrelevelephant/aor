// Package config loads atx.toml (machine list + colors).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Machine is one entry in atx.toml's [[machine]] array.
type Machine struct {
	Name       string `toml:"name"`
	Display    string `toml:"display"`
	Color      string `toml:"color"`
	SSHHost    string `toml:"ssh_host"`
	SSHUser    string `toml:"ssh_user"`
	AutoCreate bool   `toml:"auto_create"`
}

type Config struct {
	Machines []Machine `toml:"machine"`
}

// DefaultConfigPath returns ~/.config/atx/atx.toml.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".config", "atx", "atx.toml"), nil
}

// Load reads atx.toml from path. A missing file returns an empty Config and
// no error — the caller decides whether running with zero machines is OK.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	for i, m := range cfg.Machines {
		if m.Name == "" {
			return nil, fmt.Errorf("machine[%d]: name required", i)
		}
		if m.Display == "" {
			cfg.Machines[i].Display = m.Name
		}
		if m.SSHHost == "" {
			cfg.Machines[i].SSHHost = m.Name
		}
	}

	return &cfg, nil
}
