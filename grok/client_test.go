package grok_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/grok"
)

func TestClientListsAndLoadsUsingSessionCookie(t *testing.T) {
	var cookies []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookies = append(cookies, request.Header.Get("Cookie"))
		switch {
		case strings.HasPrefix(request.URL.Path, "/rest/app-chat/conversations/c1/response-node"):
			writeJSON(writer, map[string]any{"conversation": map[string]any{"id": "c1", "title": "A chat"}, "nodes": []any{map[string]any{"responseId": "r1"}, map[string]any{"responseId": "r2"}}})
		case strings.HasPrefix(request.URL.Path, "/rest/app-chat/conversations/c1/load-responses"):
			var body struct {
				ResponseIDs []string `json:"responseIds"`
			}
			json.NewDecoder(request.Body).Decode(&body)
			responses := make([]map[string]string, 0, len(body.ResponseIDs))
			for _, id := range body.ResponseIDs {
				responses = append(responses, map[string]string{"id": id, "author": "assistant", "content": id})
			}
			writeJSON(writer, map[string]any{"responses": responses})
		case request.URL.Path == "/rest/app-chat/conversations":
			writeJSON(writer, map[string]any{"conversations": []any{map[string]string{"id": "c1", "title": "A chat"}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := fixtureClient(t, server.URL)
	list, err := client.ListAllConversations(60)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "A chat" {
		t.Fatalf("unexpected list: %#v", list)
	}
	thread, err := client.LoadConversation("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Responses) != 2 {
		t.Fatalf("got %d responses", len(thread.Responses))
	}
	for _, cookie := range cookies {
		if !strings.Contains(cookie, "sso=test") {
			t.Fatalf("missing session cookie in %q", cookie)
		}
	}
}

func TestAuthFailureIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "no", http.StatusUnauthorized)
	}))
	defer server.Close()
	if err := fixtureClient(t, server.URL).Verify(); err != grok.ErrAuthExpired {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestClientDoesNotLeakSessionHeadersAcrossOriginsOrRedirects(t *testing.T) {
	var received []string
	external := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received = append(received, request.Header.Get("X-Challenge"))
		writer.Write([]byte("media"))
	}))
	defer external.Close()

	base := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect" {
			http.Redirect(writer, request, external.URL+"/asset", http.StatusFound)
			return
		}
		writer.Write([]byte("media"))
	}))
	defer base.Close()

	parsed, err := url.Parse(base.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := grok.NewClient(auth.Session{
		Cookies: []auth.Cookie{{Name: "sso", Value: "test", Domain: parsed.Hostname()}},
		Headers: map[string]string{"X-Challenge": "secret", "Authorization": "must-not-be-forwarded"},
	}, base.URL)
	if err != nil {
		t.Fatal(err)
	}
	if client.Headers.Get("Authorization") != "" {
		t.Fatal("non-browser session header was accepted")
	}

	for _, target := range []string{external.URL + "/direct", base.URL + "/redirect"} {
		response, err := client.GetMedia(target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	for _, value := range received {
		if value != "" {
			t.Fatalf("session header leaked cross-origin: %q", value)
		}
	}
}

func TestClientRejectsCookieForUnrelatedDomain(t *testing.T) {
	_, err := grok.NewClient(auth.Session{
		Cookies: []auth.Cookie{{Name: "sso", Value: "test", Domain: ".example.invalid"}},
	}, "https://grok.com")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected domain mismatch, got %v", err)
	}
}

func fixtureClient(t *testing.T, rawURL string) *grok.Client {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	session := auth.Session{Cookies: []auth.Cookie{{Name: "sso", Value: "test", Domain: parsed.Hostname()}}}
	client, err := grok.NewClient(session, rawURL)
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
