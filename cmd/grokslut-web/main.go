// grokslut-web is a loopback-only UI over the same Go client and session file
// used by the CLI. It never sends a session to a third party.
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/exporter"
	"github.com/Megatherium/grokslut/grok"
	"github.com/Megatherium/grokslut/store"
)

//go:embed page.html
var page string

type app struct {
	mu                     sync.RWMutex
	client                 *grok.Client
	sessionPath, exportDir string
	verify                 func(*grok.Client) error
}
type project struct {
	ID            string                     `json:"id"`
	Title         string                     `json:"title"`
	Conversations []grok.ConversationSummary `json:"conversations"`
}
type exportRequest struct {
	IDs    []string        `json:"ids"`
	Format exporter.Format `json:"format"`
}

func main() {
	defaultSession, _ := store.DefaultSessionPath()
	address := flag.String("listen", "127.0.0.1:8787", "loopback address for the web UI")
	sessionPath := flag.String("session", defaultSession, "shared CLI session file")
	exportDir := flag.String("exports", "exports", "export directory")
	flag.Parse()
	if !isLoopback(*address) {
		log.Fatal("--listen must use localhost or a loopback address")
	}
	a := &app{sessionPath: *sessionPath, exportDir: *exportDir, verify: func(client *grok.Client) error { return client.Verify() }}
	if session, err := store.Load(a.sessionPath); err == nil {
		if client, createErr := grok.NewClient(session, "https://grok.com"); createErr == nil {
			a.client = client
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.index)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("POST /api/session", a.saveSession)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/conversations", a.conversations)
	mux.HandleFunc("POST /api/export", a.export)
	mux.HandleFunc("POST /api/export-stream", a.exportStream)
	mux.Handle("GET /files/", http.StripPrefix("/files/", http.FileServer(http.Dir(a.exportDir))))
	server := &http.Server{Addr: *address, Handler: secureHeaders(mux)}
	log.Printf("grokslut web UI listening at http://%s", *address)
	log.Fatal(server.ListenAndServe())
}

func (a *app) index(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(writer, page)
}
func (a *app) status(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": a.getClient() != nil})
}
func (a *app) saveSession(writer http.ResponseWriter, request *http.Request) {
	if !trustedOrigin(request) {
		writeError(writer, http.StatusForbidden, errors.New("untrusted session bridge origin"))
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	defer request.Body.Close()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	session, err := auth.FromJSON(raw)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	client, err := grok.NewClient(session, "https://grok.com")
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	if a.verify != nil {
		if err := a.verify(client); err != nil {
			writeClientError(writer, err)
			return
		}
	}
	if err := store.Save(a.sessionPath, session); err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	a.mu.Lock()
	a.client = client
	a.mu.Unlock()
	writeJSON(writer, http.StatusCreated, map[string]bool{"ok": true})
}
func (a *app) logout(writer http.ResponseWriter, request *http.Request) {
	if !trustedOrigin(request) {
		writeError(writer, http.StatusForbidden, errors.New("untrusted request origin"))
		return
	}
	if err := store.Clear(a.sessionPath); err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	a.mu.Lock()
	a.client = nil
	a.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}
func (a *app) conversations(writer http.ResponseWriter, _ *http.Request) {
	client := a.getClient()
	if client == nil {
		writeError(writer, http.StatusUnauthorized, grok.ErrAuthExpired)
		return
	}
	conversations, err := client.ListAllConversations(60)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, tree(conversations))
}
func (a *app) export(writer http.ResponseWriter, request *http.Request) {
	if !trustedOrigin(request) {
		writeError(writer, http.StatusForbidden, errors.New("untrusted request origin"))
		return
	}
	client := a.getClient()
	if client == nil {
		writeError(writer, http.StatusUnauthorized, grok.ErrAuthExpired)
		return
	}
	var input exportRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	if len(input.IDs) == 0 {
		writeError(writer, http.StatusBadRequest, errors.New("select at least one conversation"))
		return
	}
	result, err := (exporter.Exporter{Client: client}).Export(input.IDs, input.Format, a.exportDir, nil)
	if err != nil {
		writeClientError(writer, err)
		return
	}
	if err := a.relativize(&result); err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *app) exportStream(writer http.ResponseWriter, request *http.Request) {
	if !trustedOrigin(request) {
		writeError(writer, http.StatusForbidden, errors.New("untrusted request origin"))
		return
	}
	client := a.getClient()
	if client == nil {
		writeError(writer, http.StatusUnauthorized, grok.ErrAuthExpired)
		return
	}
	var input exportRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	if len(input.IDs) == 0 {
		writeError(writer, http.StatusBadRequest, errors.New("select at least one conversation"))
		return
	}

	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(writer)
	flusher, _ := writer.(http.Flusher)
	send := func(value any) {
		_ = encoder.Encode(value)
		if flusher != nil {
			flusher.Flush()
		}
	}
	result, err := (exporter.Exporter{Client: client}).Export(input.IDs, input.Format, a.exportDir, func(progress exporter.Progress) {
		send(map[string]any{"type": "progress", "progress": progress})
	})
	if err != nil {
		send(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	if err := a.relativize(&result); err != nil {
		send(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	send(map[string]any{"type": "result", "result": result})
}

func (a *app) relativize(result *exporter.Result) error {
	for index, item := range result.Paths {
		relative, err := filepath.Rel(a.exportDir, item)
		if err != nil || strings.HasPrefix(relative, "..") {
			return errors.New("invalid export path")
		}
		result.Paths[index] = filepath.ToSlash(relative)
	}
	return nil
}
func (a *app) getClient() *grok.Client { a.mu.RLock(); defer a.mu.RUnlock(); return a.client }
func tree(conversations []grok.ConversationSummary) []project {
	groups := map[string]*project{}
	for _, conversation := range conversations {
		id, title := conversation.ProjectID, conversation.ProjectTitle
		if id == "" {
			id, title = "unfiled", "Unfiled conversations"
		}
		if title == "" {
			title = "Project " + id
		}
		if groups[id] == nil {
			groups[id] = &project{ID: id, Title: title}
		}
		groups[id].Conversations = append(groups[id].Conversations, conversation)
	}
	result := make([]project, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.Conversations, func(i, j int) bool { return group.Conversations[i].UpdatedAt > group.Conversations[j].UpdatedAt })
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Title < result[j].Title })
	return result
}
func trustedOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	return origin == "" || strings.HasPrefix(origin, "chrome-extension://") || origin == "http://"+request.Host
}
func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
}
func writeClientError(writer http.ResponseWriter, err error) {
	if errors.Is(err, grok.ErrAuthExpired) {
		writeError(writer, http.StatusUnauthorized, err)
		return
	}
	writeError(writer, http.StatusBadGateway, err)
}
func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}
