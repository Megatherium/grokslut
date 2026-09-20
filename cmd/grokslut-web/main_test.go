package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Megatherium/grokslut/grok"
	"github.com/Megatherium/grokslut/store"
)

type countingProviderClient struct{ listCalls int }

func (c *countingProviderClient) ListAllConversations(int) ([]grok.ConversationSummary, error) {
	c.listCalls++
	return []grok.ConversationSummary{{ID: "one", Title: "Cached title"}}, nil
}
func (*countingProviderClient) LoadConversationProgress(string, grok.LoadProgress) (grok.Thread, error) {
	return grok.Thread{}, nil
}
func (*countingProviderClient) GetMedia(string) (*http.Response, error) { return nil, nil }
func (*countingProviderClient) Verify() error                           { return nil }

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
	if err := json.NewDecoder(status.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
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

func TestTrustedOriginAcceptsValidExtensionOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/session", nil)
	request.Header.Set("Origin", "chrome-extension://jphdifcbknaafldncdpcigahoomemckk")
	if !trustedOrigin(request) {
		t.Fatal("rejected a valid Chrome extension origin")
	}
	request.Header.Set("Origin", "chrome-extension://jphdifcbknaafldncdpcigahoomemckk/path")
	if trustedOrigin(request) {
		t.Fatal("accepted an extension origin with a path")
	}
}

func TestFilesServesRegularFilesWithoutDirectoryListing(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "export.json"), []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{exportDir: directory}

	listing := httptest.NewRecorder()
	a.files(listing, httptest.NewRequest(http.MethodGet, "/files/", nil))
	if listing.Code != http.StatusNotFound {
		t.Fatalf("directory listing status = %d, want 404", listing.Code)
	}

	file := httptest.NewRecorder()
	a.files(file, httptest.NewRequest(http.MethodGet, "/files/export.json", nil))
	if file.Code != http.StatusOK || file.Body.String() != `{"ok":true}` {
		t.Fatalf("file response = %d %q", file.Code, file.Body.String())
	}
}

func TestConversationsCacheUntilExplicitRefresh(t *testing.T) {
	client := &countingProviderClient{}
	a := &app{clients: map[string]providerClient{"gemini": client}, conversationCache: map[string][]project{}}
	for _, target := range []string{
		"/api/conversations?provider=gemini",
		"/api/conversations?provider=gemini",
		"/api/conversations?provider=gemini&refresh=1",
	} {
		response := httptest.NewRecorder()
		a.conversations(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", target, response.Code, response.Body.String())
		}
	}
	if client.listCalls != 2 {
		t.Fatalf("list calls = %d, want initial load plus explicit refresh", client.listCalls)
	}
}

func TestExportEndpointWritesAndRelativizesResult(t *testing.T) {
	exportDir := t.TempDir()
	client := &countingProviderClient{}
	a := &app{clients: map[string]providerClient{"grok": client}, exportDir: exportDir}
	request := httptest.NewRequest(http.MethodPost, "/api/export", bytes.NewBufferString(`{"ids":["one"],"format":"json"}`))
	response := httptest.NewRecorder()
	a.export(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || filepath.IsAbs(result.Paths[0]) {
		t.Fatalf("unexpected paths: %#v", result.Paths)
	}
	if _, err := os.Stat(filepath.Join(exportDir, filepath.FromSlash(result.Paths[0]))); err != nil {
		t.Fatalf("exported file unavailable: %v", err)
	}
}
