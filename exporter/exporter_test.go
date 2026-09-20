package exporter_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
			_, _ = writer.Write([]byte("png"))
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
	defer func() { _ = reader.Close() }()
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
	_ = json.NewEncoder(writer).Encode(value)
}

type staticHistoryClient struct{ thread grok.Thread }

func (client staticHistoryClient) LoadConversationProgress(string, grok.LoadProgress) (grok.Thread, error) {
	return client.thread, nil
}
func (staticHistoryClient) GetMedia(string) (*http.Response, error) { return nil, nil }

type mappedHistoryClient struct{ threads map[string]grok.Thread }

func (client mappedHistoryClient) LoadConversationProgress(id string, _ grok.LoadProgress) (grok.Thread, error) {
	return client.threads[id], nil
}
func (mappedHistoryClient) GetMedia(string) (*http.Response, error) { return nil, nil }

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

func TestZIPDeduplicatesSameTitleDirectories(t *testing.T) {
	client := mappedHistoryClient{threads: map[string]grok.Thread{
		"one": {Conversation: grok.ConversationSummary{ID: "one", Title: "Same title"}},
		"two": {Conversation: grok.ConversationSummary{ID: "two", Title: "Same title"}},
	}}
	result, err := (exporter.Exporter{Client: client}).Export([]string{"one", "two"}, exporter.ZIP, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(result.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	seen := map[string]bool{}
	for _, file := range reader.File {
		if seen[file.Name] {
			t.Fatalf("duplicate ZIP entry %q", file.Name)
		}
		seen[file.Name] = true
	}
	for _, name := range []string{"Same title/Same title.json", "Same title-2/Same title.json"} {
		if !seen[name] {
			t.Fatalf("missing %q in archive entries %#v", name, seen)
		}
	}
}

func TestSafeNameTruncatesAtUTF8Boundary(t *testing.T) {
	name := exporter.SafeName(strings.Repeat("a", 79) + "é")
	if !utf8.ValidString(name) {
		t.Fatalf("safe name is invalid UTF-8: %q", name)
	}
	if len(name) > 80 {
		t.Fatalf("safe name is %d bytes, want at most 80", len(name))
	}
}

func TestMediaURLsRequiresExactMediaKey(t *testing.T) {
	raw := json.RawMessage(`{"mediaCount":"https://example.invalid/not-media","assetUrls":["https://example.invalid/asset"],"metadata":{"attachment":"https://example.invalid/file"}}`)
	urls := exporter.MediaURLs(raw)
	sort.Strings(urls)
	if len(urls) != 2 || urls[0] != "https://example.invalid/asset" || urls[1] != "https://example.invalid/file" {
		t.Fatalf("unexpected media URLs: %#v", urls)
	}
}

type oversizedMediaClient struct{ thread grok.Thread }

func (client oversizedMediaClient) LoadConversationProgress(string, grok.LoadProgress) (grok.Thread, error) {
	return client.thread, nil
}
func (oversizedMediaClient) GetMedia(string) (*http.Response, error) {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader("not read")),
		ContentLength: (64 << 20) + 1,
	}, nil
}

func TestOversizedMediaIsSkippedWithWarning(t *testing.T) {
	assetURL := "https://example.invalid/private/image.png?token=secret"
	client := oversizedMediaClient{thread: grok.Thread{
		Conversation: grok.ConversationSummary{ID: "one", Title: "Media"},
		Responses:    []json.RawMessage{json.RawMessage(`{"id":"r1","message":"body","assetUrls":["` + assetURL + `"]}`)},
	}}
	result, err := (exporter.Exporter{Client: client}).Export([]string{"one"}, exporter.Markdown, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "exceeds 64 MiB") {
		t.Fatalf("unexpected warnings: %#v", result.Warnings)
	}
	if strings.Contains(result.Warnings[0], "token=secret") {
		t.Fatalf("warning leaked media query: %q", result.Warnings[0])
	}
	if len(result.Paths) != 1 {
		t.Fatalf("oversized media was written: %#v", result.Paths)
	}
}

func TestExportUsesOwnerOnlyPermissions(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "exports")
	client := staticHistoryClient{thread: grok.Thread{Conversation: grok.ConversationSummary{ID: "one", Title: "Private"}}}
	result, err := (exporter.Exporter{Client: client}).Export([]string{"one"}, exporter.JSON, outDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outDir, filepath.Dir(result.Paths[0])} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0700 {
			t.Fatalf("directory %s mode = %o, want 700", path, got)
		}
	}
	info, err := os.Stat(result.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("export mode = %o, want 600", got)
	}
}

func TestRenderMarkdownPreservesDisplayNameAndSortsParsedTimes(t *testing.T) {
	thread := grok.Thread{
		Conversation: grok.ConversationSummary{ID: "one", Title: "Ordering"},
		Responses: []json.RawMessage{
			json.RawMessage(`{"id":"later","author":"iOS","message":"Later","createTime":"2025-12-31T23:30:00Z"}`),
			json.RawMessage(`{"id":"earlier","author":"McDonald","message":"Earlier","createTime":"2026-01-01T01:00:00+02:00"}`),
		},
	}
	markdown := exporter.RenderMarkdown(thread, nil)
	if !strings.Contains(markdown, "## McDonald") || !strings.Contains(markdown, "## iOS") {
		t.Fatalf("display names were changed:\n%s", markdown)
	}
	if strings.Index(markdown, "Earlier") > strings.Index(markdown, "Later") {
		t.Fatalf("responses were not sorted by parsed time:\n%s", markdown)
	}
}

type cancelableHistoryClient struct{}

func (cancelableHistoryClient) LoadConversationProgress(string, grok.LoadProgress) (grok.Thread, error) {
	return grok.Thread{}, nil
}
func (cancelableHistoryClient) GetMedia(string) (*http.Response, error) { return nil, nil }
func (cancelableHistoryClient) LoadConversationProgressContext(ctx context.Context, _ string, _ grok.LoadProgress) (grok.Thread, error) {
	<-ctx.Done()
	return grok.Thread{}, ctx.Err()
}
func (cancelableHistoryClient) GetMediaContext(context.Context, string) (*http.Response, error) {
	return nil, nil
}

func TestExportContextCancelsLoad(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := (exporter.Exporter{Client: cancelableHistoryClient{}}).ExportContext(ctx, []string{"one"}, exporter.JSON, t.TempDir(), nil)
	if err != context.DeadlineExceeded {
		t.Fatalf("got %v, want context deadline", err)
	}
}

type cancelMediaClient struct{ thread grok.Thread }

func (client cancelMediaClient) LoadConversationProgress(string, grok.LoadProgress) (grok.Thread, error) {
	return client.thread, nil
}
func (cancelMediaClient) GetMedia(string) (*http.Response, error) { return nil, nil }
func (client cancelMediaClient) LoadConversationProgressContext(context.Context, string, grok.LoadProgress) (grok.Thread, error) {
	return client.thread, nil
}
func (cancelMediaClient) GetMediaContext(ctx context.Context, _ string) (*http.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCanceledZIPRemovesPartialArchive(t *testing.T) {
	outDir := t.TempDir()
	client := cancelMediaClient{thread: grok.Thread{
		Conversation: grok.ConversationSummary{ID: "one", Title: "Canceled"},
		Responses:    []json.RawMessage{json.RawMessage(`{"assetUrls":["https://example.invalid/media.png"]}`)},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := (exporter.Exporter{Client: client}).ExportContext(ctx, []string{"one"}, exporter.ZIP, outDir, nil)
	if err != context.DeadlineExceeded {
		t.Fatalf("got %v, want context deadline", err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial archive was left behind: %#v", entries)
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
