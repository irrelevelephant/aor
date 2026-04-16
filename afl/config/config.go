package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RemoteConfig describes a remote afl server.
type RemoteConfig struct {
	URL       string `json:"url"`
	Workspace string `json:"workspace,omitempty"`
}

// Config holds afl configuration, primarily remote workspace mappings.
type Config struct {
	Remotes          map[string]RemoteConfig `json:"remotes"`
	DefaultRemote    string                  `json:"default_remote,omitempty"`
	DefaultWorkspace string                  `json:"default_workspace,omitempty"`
	Workspaces       map[string]string       `json:"workspaces,omitempty"`
}

// ResolveWorkspaceDir checks directory mappings for dir and mainWorktree,
// then falls back to DefaultWorkspace. Returns "" if nothing is configured.
func (c Config) ResolveWorkspaceDir(dir, mainWorktree string) string {
	if c.Workspaces != nil {
		if ws, ok := c.Workspaces[dir]; ok {
			return ws
		}
		if mainWorktree != "" && mainWorktree != dir {
			if ws, ok := c.Workspaces[mainWorktree]; ok {
				return ws
			}
		}
	}
	return c.DefaultWorkspace
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

// ResolveRemote looks up a remote for the given workspace.
// Checks exact match first, then falls back to DefaultRemote.
func (c Config) ResolveRemote(workspace string) *RemoteConfig {
	if c.Remotes == nil {
		return nil
	}
	if r, ok := c.Remotes[workspace]; ok {
		return &r
	}
	if c.DefaultRemote != "" {
		if r, ok := c.Remotes[c.DefaultRemote]; ok {
			return &r
		}
	}
	return nil
}
