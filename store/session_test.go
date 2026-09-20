package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/store"
)

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "session.json")
	session := auth.Session{Cookies: []auth.Cookie{{Name: "sso", Value: "private", Domain: ".grok.com"}}}
	if err := store.Save(path, session); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("session mode = %o, want 600", got)
	}
}

func TestSaveCleansTemporaryFileAfterRenameFailure(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "existing-directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	err := store.Save(target, auth.Session{})
	if err == nil || !strings.Contains(err.Error(), "replace session") {
		t.Fatalf("expected replace failure, got %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "existing-directory" {
		t.Fatalf("temporary file was not cleaned up: %#v", entries)
	}
}
