// Package push owns atx's Web Push (VAPID) keypair and delivery.
package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// VAPID is the persistent keypair atx uses for Web Push. Both keys are
// urlsafe-base64-encoded as required by the spec.
type VAPID struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	Subject    string `json:"subject"` // mailto:... or https://... — required by spec
}

// DefaultVAPIDPath returns ~/.config/atx/vapid.json.
func DefaultVAPIDPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "atx", "vapid.json"), nil
}

// LoadOrCreate reads the keypair at path; if no file exists, generates a
// fresh pair, persists it, and returns the new keys.
func LoadOrCreate(path, subject string) (*VAPID, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var v VAPID
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if v.Subject == "" {
			v.Subject = subject
		}
		return &v, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("generate vapid: %w", err)
	}
	v := &VAPID{PrivateKey: priv, PublicKey: pub, Subject: subject}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create vapid dir: %w", err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return v, nil
}
