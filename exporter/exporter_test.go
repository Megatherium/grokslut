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

func TestRenderMarkdownHydratesGrokMessageStringsInCreateTimeOrder(t *testing.T) {
	thread := grok.Thread{
		Conversation: grok.ConversationSummary{ID: "c1", Title: "Hydrated chat"},
		Responses: []json.RawMessage{
			json.RawMessage(`{"responseId":"later","sender":"assistant","message":"Second turn","createTime":"2026-01-02T00:00:00Z"}`),
			json.RawMessage(`{"responseId":"earlier","sender":"human","message":"First turn","createTime":"2026-01-01T00:00:00Z"}`),
			json.RawMessage(`{"responseId":"partial-stub","sender":"assistant","message":"","partial":true,"createTime":"2026-01-01T12:00:00Z"}`),
		},
	}

	markdown := exporter.RenderMarkdown(thread, nil)
	if strings.Contains(markdown, "No text content") {
		t.Fatalf("Grok message strings were not hydrated:\n%s", markdown)
	}
	if strings.Contains(markdown, "partial-stub") {
		t.Fatalf("bodyless response stub leaked into Markdown:\n%s", markdown)
	}
	first, second := strings.Index(markdown, "First turn"), strings.Index(markdown, "Second turn")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("responses not rendered in createTime order:\n%s", markdown)
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

type staticHistoryClient struct{ thread grok.Thread }

func (client staticHistoryClient) LoadConversationProgress(string, grok.LoadProgress) (grok.Thread, error) {
	return client.thread, nil
}
func (staticHistoryClient) GetMedia(string) (*http.Response, error) { return nil, nil }

func TestJSONIsNormalizedAndRawJSONPreservesProviderPayload(t *testing.T) {
	thread := grok.Thread{
		Conversation: grok.ConversationSummary{
			Provider:  "gemini",
			ID:        "c1",
			Title:     "JSON formats",
			UpdatedAt: "2026-09-16T00:00:00Z",
			Raw:       json.RawMessage(`["c1","JSON formats",null,null,null,[1789516800,0]]`),
		},
		Responses: []json.RawMessage{
			json.RawMessage(`{"provider":"gemini","responseId":"later","parentResponseId":"earlier","sender":"assistant","message":"Second","createTime":"2026-09-16T00:00:02Z"}`),
			json.RawMessage(`{"provider":"gemini","responseId":"earlier","parentResponseId":"","sender":"human","message":"First","createTime":"2026-09-16T00:00:01Z","geminiTurn":[null,[null]]}`),
		},
	}
	exporterInstance := exporter.Exporter{Client: staticHistoryClient{thread: thread}}

	cleanResult, err := exporterInstance.Export([]string{"c1"}, exporter.JSON, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := os.ReadFile(cleanResult.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clean), "geminiTurn") || strings.Contains(string(clean), `"conversation": [`) || strings.Contains(string(clean), "null") {
		t.Fatalf("normalized JSON retained raw positional data:\n%s", clean)
	}
	var normalized struct {
		Conversation grok.ConversationSummary `json:"conversation"`
		Responses    []map[string]any         `json:"responses"`
	}
	if err := json.Unmarshal(clean, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.Conversation.Provider != "gemini" || len(normalized.Responses) != 2 || normalized.Responses[0]["responseId"] != "earlier" {
		t.Fatalf("unexpected normalized JSON: %#v", normalized)
	}

	rawResult, err := exporterInstance.Export([]string{"c1"}, exporter.RawJSON, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(rawResult.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rawResult.Paths[0], ".raw.json") || !strings.Contains(string(raw), "geminiTurn") || !strings.Contains(string(raw), `"conversation":[`) {
		t.Fatalf("raw JSON did not preserve provider payload: %s", raw)
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
