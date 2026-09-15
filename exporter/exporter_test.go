package exporter_test

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/exporter"
	"github.com/Megatherium/grokslut/grok"
)

func TestExportZIPIncludesMarkdownJSONAndMedia(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.Contains(request.URL.Path, "response-node"):
			writeJSON(writer, map[string]any{"conversation": map[string]string{"id": "c1", "title": "../../bad: title"}, "nodes": []any{map[string]string{"responseId": "r1"}}})
		case strings.Contains(request.URL.Path, "load-responses"):
			writeJSON(writer, map[string]any{"responses": []any{map[string]any{"id": "r1", "author": "assistant", "content": "Look " + server.URL + "/media/image.png", "generatedImageUrls": []string{server.URL + "/media/image.png"}}}})
		case request.URL.Path == "/media/image.png":
			writer.Header().Set("Content-Type", "image/png")
			writer.Write([]byte("png"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := fixtureClient(t, server.URL)
	directory := t.TempDir()
	result, err := (exporter.Exporter{Client: client}).Export([]string{"c1"}, exporter.ZIP, directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	reader, err := zip.OpenReader(result.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	joined := strings.Join(names, "\n")
	if !strings.Contains(joined, ".md") || !strings.Contains(joined, ".json") || !strings.Contains(joined, "media/") {
		t.Fatalf("incomplete archive: %v", names)
	}
}

func TestSafeNameCannotEscapeDestination(t *testing.T) {
	if got := exporter.SafeName(`../../not allowed?`); got != "not allowed_" {
		t.Fatalf("unexpected safe name %q", got)
	}
	if filepath.IsAbs(exporter.SafeName(`/escape`)) {
		t.Fatal("safe name became absolute")
	}
}

func fixtureClient(t *testing.T, rawURL string) *grok.Client {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := grok.NewClient(auth.Session{Cookies: []auth.Cookie{{Name: "sso", Value: "test", Domain: parsed.Hostname()}}}, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	client.Retries = 0
	return client
}
func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(value)
}
func TestMain(m *testing.M) { os.Exit(m.Run()) }
