package gemini_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/gemini"
	"github.com/Megatherium/grokslut/grok"
)

func TestClientListsAndFullyPaginatesHydratedConversation(t *testing.T) {
	loadCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/app":
			fmt.Fprint(writer, `<script>window.WIZ_global_data={"SNlM0e":"csrf","cfb2h":"build","FdrFJe":"sid"};</script>`)
		case "/_/BardChatUi/data/batchexecute":
			if !strings.Contains(request.Header.Get("Cookie"), "gemini=test") {
				t.Fatal("Gemini session cookie missing")
			}
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			rpc := request.URL.Query().Get("rpcids")
			switch rpc {
			case "MaZiqc":
				batchResponse(writer, rpc, []any{nil, "next", []any{[]any{"c_one", "A Gemini chat", nil, nil, nil, []any{1700000000, 5}}}})
			case "hNvQHb":
				loadCalls++
				if loadCalls == 1 {
					batchResponse(writer, rpc, []any{[]any{turn("c_one", "r_new", "rc_old", "Newest question", "rc_new", "Newest answer", 1700000002)}, "older", nil, []any{}})
					return
				}
				batchResponse(writer, rpc, []any{[]any{turn("c_one", "r_old", "", "Oldest question", "rc_old", "Oldest answer", 1700000001)}, nil, nil, []any{}})
			default:
				http.NotFound(writer, request)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := fixtureClient(t, server.URL)
	list, err := client.ListConversations(36, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 1 || list.Conversations[0].Provider != "gemini" || list.Conversations[0].Title != "A Gemini chat" || list.NextCursor != "next" {
		t.Fatalf("unexpected list: %#v", list)
	}
	var progress [][2]int
	thread, err := client.LoadConversationProgress("c_one", func(loaded, total int) {
		progress = append(progress, [2]int{loaded, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	if loadCalls != 2 || len(thread.Responses) != 4 {
		t.Fatalf("loads=%d responses=%d", loadCalls, len(thread.Responses))
	}
	encoded := string(mustJSON(t, thread.Responses))
	for _, expected := range []string{"Newest question", "Newest answer", "Oldest question", "Oldest answer", `"parentResponseId":"rc_old"`} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("missing hydrated value %q in %s", expected, encoded)
		}
	}
	if got := progress[len(progress)-1]; got != [2]int{4, 4} {
		t.Fatalf("final progress = %v", got)
	}
}

func TestMissingGeminiBootstrapTokensIsAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, "sign in")
	}))
	defer server.Close()
	if err := fixtureClient(t, server.URL).Verify(); err != gemini.ErrAuthExpired {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestListAllConversationsExhaustsPinnedAndRegularCursors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/app" {
			fmt.Fprint(writer, `<script>window.WIZ_global_data={"SNlM0e":"csrf","cfb2h":"build","FdrFJe":"sid"};</script>`)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		body := request.Form.Get("f.req")
		switch {
		case strings.Contains(body, `pin-next`):
			batchResponse(writer, "MaZiqc", listPayload("", "p2", "Pinned two"))
		case strings.Contains(body, `[1,null,1]`):
			batchResponse(writer, "MaZiqc", listPayload("pin-next", "p1", "Pinned one"))
		case strings.Contains(body, `regular-next`):
			batchResponse(writer, "MaZiqc", listPayload("", "r2", "Regular two"))
		default:
			batchResponse(writer, "MaZiqc", listPayload("regular-next", "r1", "Regular one"))
		}
	}))
	defer server.Close()

	conversations, err := fixtureClient(t, server.URL).ListAllConversations(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 4 {
		t.Fatalf("got %d conversations: %#v", len(conversations), conversations)
	}
	for _, id := range []string{"p1", "p2", "r1", "r2"} {
		if !containsConversation(conversations, id) {
			t.Fatalf("missing %s from %#v", id, conversations)
		}
	}
}

func listPayload(cursor, id, title string) []any {
	var cursorValue any
	if cursor != "" {
		cursorValue = cursor
	}
	return []any{nil, cursorValue, []any{[]any{id, title, nil, nil, nil, []any{1700000000, 0}}}}
}

func containsConversation(conversations []grok.ConversationSummary, id string) bool {
	for _, conversation := range conversations {
		if conversation.ID == id {
			return true
		}
	}
	return false
}

func turn(conversationID, humanID, parentID, humanText, assistantID, assistantText string, seconds int64) []any {
	var parent any
	if parentID != "" {
		parent = []any{conversationID, "older-request", parentID}
	}
	return []any{
		[]any{conversationID, humanID},
		parent,
		[]any{[]any{humanText}},
		[]any{[]any{[]any{assistantID, []any{assistantText}}}},
		[]any{seconds, 0},
	}
}

func batchResponse(writer http.ResponseWriter, rpc string, payload any) {
	inner, _ := json.Marshal(payload)
	outer, _ := json.Marshal([]any{[]any{"wrb.fr", rpc, string(inner), nil, nil, nil, "generic"}})
	fmt.Fprintf(writer, ")]}'\n\n%d\n%s\n", len(outer), outer)
}

func fixtureClient(t *testing.T, rawURL string) *gemini.Client {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := gemini.NewClient(auth.Session{Cookies: []auth.Cookie{{Name: "gemini", Value: "test", Domain: parsed.Hostname()}}}, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
