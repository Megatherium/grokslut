// Package store persists a private session envelope for the CLI and local web UI.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Megatherium/grokslut/auth"
)

func DefaultSessionPath() (string, error) {
	return DefaultProviderSessionPath("grok")
}

func DefaultProviderSessionPath(provider string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := "session.json"
	if provider != "" && provider != "grok" {
		name = "session-" + provider + ".json"
	}
	return filepath.Join(dir, "grokslut", name), nil
}

func Load(path string) (auth.Session, error) { return auth.FromFile(path) }

// Save atomically replaces the session with owner-only file permissions.
func Save(path string, session auth.Session) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace session: %w", err)
	}
	return nil
}

func Clear(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
