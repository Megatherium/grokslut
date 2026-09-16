package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Megatherium/grokslut/grok"
	"github.com/Megatherium/grokslut/store"
)

func TestSaveSessionCreatesSharedSessionAndStatus(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	app := &app{sessionPaths: map[string]string{"grok": sessionPath}, clients: map[string]providerClient{}, exportDir: t.TempDir()}
	request := httptest.NewRequest(http.MethodPost, "/api/session", bytes.NewBufferString(`{"cookies":[{"name":"sso","value":"private","domain":".grok.com"}]}`))
	response := httptest.NewRecorder()
	app.saveSession(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("save status %d: %s", response.Code, response.Body.String())
	}
	if _, err := store.Load(sessionPath); err != nil {
		t.Fatalf("saved session unavailable: %v", err)
	}
	status := httptest.NewRecorder()
	app.status(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var result map[string]any
	json.NewDecoder(status.Body).Decode(&result)
	if result["authenticated"] != true {
		t.Fatalf("unexpected status: %#v", result)
	}
}

func TestTreeGroupsProjectAndUnfiledConversations(t *testing.T) {
	groups := tree([]grok.ConversationSummary{{ID: "one", Title: "Under project", ProjectID: "p1", ProjectTitle: "Project One"}, {ID: "two", Title: "Unfiled"}})
	if len(groups) != 2 {
		t.Fatalf("got %d groups", len(groups))
	}
	var foundProject, foundUnfiled bool
	for _, group := range groups {
		if group.Title == "Project One" {
			foundProject = true
		}
		if group.Title == "Unfiled conversations" {
			foundUnfiled = true
		}
	}
	if !foundProject || !foundUnfiled {
		t.Fatalf("wrong groups: %#v", groups)
	}
}

func TestSessionBridgeRejectsCrossSiteOrigin(t *testing.T) {
	a := &app{sessionPaths: map[string]string{"grok": filepath.Join(t.TempDir(), "session.json")}, clients: map[string]providerClient{}}
	request := httptest.NewRequest(http.MethodPost, "/api/session", bytes.NewBufferString(`{"cookies":[]}`))
	request.Header.Set("Origin", "https://example.invalid")
	response := httptest.NewRecorder()
	a.saveSession(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("got status %d", response.Code)
	}
}
