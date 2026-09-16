// Package grok contains the HTTP client for the observed, unofficial Grok
// history endpoints. The endpoint response shapes are deliberately decoded
// defensively because the service may change without notice.
package grok

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Megatherium/grokslut/auth"
)

var ErrAuthExpired = errors.New("Grok session expired or was rejected; sign in again and refresh the session")

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Grok request failed (HTTP %d)", e.Status) }

type Client struct {
	BaseURL *url.URL
	HTTP    *http.Client
	Headers http.Header
	Retries int
}

func NewClient(session auth.Session, baseURL string) (*Client, error) {
	if baseURL == "" {
		baseURL = "https://grok.com"
	}
	base, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return nil, err
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" {
		return nil, fmt.Errorf("base URL must be HTTP(S) with a host")
	}
	headers := make(http.Header, len(session.Headers))
	for key, value := range session.Headers {
		if allowedSessionHeader(key) {
			headers.Set(key, value)
		}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Jar:     jar,
		Timeout: 45 * time.Second,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if !sameOrigin(request.URL, base) {
				stripSessionHeaders(request.Header, headers)
			}
			return nil
		},
	}
	if err := session.Apply(httpClient, base.String()); err != nil {
		return nil, err
	}
	return &Client{BaseURL: base, HTTP: httpClient, Headers: headers, Retries: 3}, nil
}

func (c *Client) Verify() error { _, err := c.ListConversations(1, ""); return err }

func (c *Client) ListConversations(pageSize int, cursor string) (ListResult, error) {
	if pageSize < 1 {
		pageSize = 60
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := url.Values{"pageSize": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	raw, err := c.request(http.MethodGet, "/rest/app-chat/conversations?"+query.Encode(), nil)
	if err != nil {
		return ListResult{}, err
	}
	return decodeList(raw)
}

func (c *Client) ListAllConversations(pageSize int) ([]ConversationSummary, error) {
	var all []ConversationSummary
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := c.ListConversations(pageSize, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, result.Conversations...)
		if result.NextCursor == "" || result.NextCursor == cursor {
			return all, nil
		}
		cursor = result.NextCursor
	}
	return all, fmt.Errorf("conversation pagination exceeded 100 pages")
}

type LoadProgress func(loaded, total int)

func (c *Client) LoadConversation(id string) (Thread, error) {
	return c.LoadConversationProgress(id, nil)
}

// LoadConversationProgress hydrates every response named by Grok's node index,
// then follows unresolved parent links until the complete ancestry is loaded.
// This bypasses the web UI's progressive scroll-based hydration.
func (c *Client) LoadConversationProgress(id string, progress LoadProgress) (Thread, error) {
	if id == "" {
		return Thread{}, errors.New("conversation id is required")
	}
	safeID := url.PathEscape(id)
	nodes, err := c.request(http.MethodGet, "/rest/app-chat/conversations/"+safeID+"/response-node?includeThreads=true", nil)
	if err != nil {
		return Thread{}, err
	}
	responseIDs := unique(collectResponseIDs(nodes))
	thread := Thread{Conversation: conversationFromNodes(nodes, id), Nodes: nodes}
	loaded := map[string]bool{}
	attempted := map[string]bool{}
	total := len(responseIDs)
	notifyLoadProgress(progress, 0, total)

	load := func(ids []string) error {
		for _, batch := range chunk(ids, 75) {
			for _, responseID := range batch {
				attempted[responseID] = true
			}
			payload, _ := json.Marshal(map[string][]string{"responseIds": batch})
			raw, err := c.request(http.MethodPost, "/rest/app-chat/conversations/"+safeID+"/load-responses", payload)
			if err != nil {
				return err
			}
			responses, err := responseArray(raw)
			if err != nil {
				return err
			}
			for _, response := range responses {
				responseID := responseString(response, "responseId", "id", "response_id")
				if responseID != "" && loaded[responseID] {
					continue
				}
				if responseID != "" {
					loaded[responseID] = true
				}
				thread.Responses = append(thread.Responses, response)
			}
			notifyLoadProgress(progress, len(thread.Responses), total)
		}
		return nil
	}

	if err := load(responseIDs); err != nil {
		return Thread{}, err
	}
	for {
		var missing []string
		for _, response := range thread.Responses {
			parentID := responseString(response, "parentResponseId", "parentId", "parent_id")
			if parentID != "" && !loaded[parentID] && !attempted[parentID] {
				missing = append(missing, parentID)
			}
		}
		missing = unique(missing)
		if len(missing) == 0 {
			break
		}
		total += len(missing)
		notifyLoadProgress(progress, len(thread.Responses), total)
		if err := load(missing); err != nil {
			return Thread{}, err
		}
	}
	var unresolved []string
	for _, response := range thread.Responses {
		parentID := responseString(response, "parentResponseId", "parentId", "parent_id")
		if parentID != "" && !loaded[parentID] {
			unresolved = append(unresolved, parentID)
		}
	}
	if unresolved = unique(unresolved); len(unresolved) > 0 {
		return Thread{}, fmt.Errorf("Grok did not return %d referenced ancestor response(s)", len(unresolved))
	}
	return thread, nil
}

func notifyLoadProgress(progress LoadProgress, loaded, total int) {
	if progress != nil {
		progress(loaded, total)
	}
}

func responseString(raw json.RawMessage, keys ...string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	return firstString(object, keys...)
}

// GetMedia downloads an asset with the same session and browser headers.
func (c *Client) GetMedia(assetURL string) (*http.Response, error) {
	asset, err := url.Parse(assetURL)
	if err != nil || (asset.Scheme != "http" && asset.Scheme != "https") || asset.Hostname() == "" {
		return nil, fmt.Errorf("invalid media URL")
	}
	req, err := http.NewRequest(http.MethodGet, asset.String(), nil)
	if err != nil {
		return nil, err
	}
	if sameOrigin(asset, c.BaseURL) {
		for key, values := range c.Headers {
			req.Header[key] = append([]string(nil), values...)
		}
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		response.Body.Close()
		return nil, ErrAuthExpired
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &HTTPError{Status: response.StatusCode}
	}
	return response, nil
}

func allowedSessionHeader(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "x-") || lower == "user-agent" || lower == "accept-language"
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}

func stripSessionHeaders(destination, session http.Header) {
	for name := range session {
		destination.Del(name)
	}
}

func (c *Client) request(method, path string, body []byte) ([]byte, error) {
	endpoint, err := c.BaseURL.Parse(path)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt <= c.Retries; attempt++ {
		req, err := http.NewRequest(method, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for key, values := range c.Headers {
			req.Header[key] = append([]string(nil), values...)
		}
		req.Header.Set("Accept", "application/json")
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := c.HTTP.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
			response.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			if response.StatusCode == 401 || response.StatusCode == 403 {
				return nil, ErrAuthExpired
			}
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return data, nil
			}
			if !retryable(response.StatusCode) || attempt == c.Retries {
				return nil, &HTTPError{Status: response.StatusCode, Body: string(data[:min(len(data), 500)])}
			}
			time.Sleep(retryAfter(response, attempt))
			continue
		}
		if attempt == c.Retries {
			return nil, fmt.Errorf("Grok request failed after retries: %w", err)
		}
		time.Sleep(time.Duration(1<<attempt) * 500 * time.Millisecond)
	}
	return nil, errors.New("unreachable")
}

func decodeList(raw []byte) (ListResult, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ListResult{}, fmt.Errorf("invalid conversation list JSON: %w", err)
	}
	entries := firstArray(payload, "conversations", "items", "data")
	var conversations []ConversationSummary
	for _, entry := range entries {
		conversations = append(conversations, normalizeConversation(entry))
	}
	return ListResult{Conversations: conversations, NextCursor: firstString(payload, "nextCursor", "nextPageToken", "cursor"), Raw: raw}, nil
}
func responseArray(raw []byte) ([]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		return firstArray(object, "responses", "items", "data"), nil
	}
	var result []json.RawMessage
	return result, json.Unmarshal(raw, &result)
}
func normalizeConversation(raw json.RawMessage) ConversationSummary {
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	projectID, projectTitle := firstString(object, "projectId", "project_id"), ""
	if projectRaw := object["project"]; len(projectRaw) > 0 {
		var project map[string]json.RawMessage
		if json.Unmarshal(projectRaw, &project) == nil {
			if projectID == "" {
				projectID = firstString(project, "id", "projectId")
			}
			projectTitle = firstString(project, "title", "name")
		}
	}
	return ConversationSummary{ID: firstString(object, "id", "conversationId", "conversation_id"), Title: defaultString(firstString(object, "title", "name"), "Untitled conversation"), CreatedAt: firstString(object, "createdAt", "created_at"), UpdatedAt: firstString(object, "updatedAt", "updated_at"), Preview: firstString(object, "preview", "lastMessagePreview"), ProjectID: projectID, ProjectTitle: projectTitle, Raw: raw}
}
func conversationFromNodes(raw []byte, id string) ConversationSummary {
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(raw, &payload)
	candidate := payload["conversation"]
	if len(candidate) == 0 {
		candidate = raw
	}
	result := normalizeConversation(candidate)
	if result.ID == "" {
		result.ID = id
	}
	return result
}
func firstArray(object map[string]json.RawMessage, keys ...string) []json.RawMessage {
	for _, key := range keys {
		var values []json.RawMessage
		if json.Unmarshal(object[key], &values) == nil && values != nil {
			return values
		}
	}
	return nil
}
func firstString(object map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var result string
		if json.Unmarshal(object[key], &result) == nil && result != "" {
			return result
		}
	}
	return ""
}
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func retryable(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}
func retryAfter(response *http.Response, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
		return minDuration(time.Duration(seconds)*time.Second, 30*time.Second)
	}
	return time.Duration(1<<attempt) * 500 * time.Millisecond
}
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
func chunk(values []string, size int) [][]string {
	var result [][]string
	for len(values) > 0 {
		n := size
		if len(values) < n {
			n = len(values)
		}
		result = append(result, values[:n])
		values = values[n:]
	}
	return result
}
func unique(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
func collectResponseIDs(raw []byte) []string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var result []string
	var visit func(any)
	visit = func(current any) {
		switch item := current.(type) {
		case map[string]any:
			for key, value := range item {
				if key == "responseId" || key == "response_id" {
					if id, ok := value.(string); ok {
						result = append(result, id)
					}
				}
				visit(value)
			}
		case []any:
			for _, value := range item {
				visit(value)
			}
		}
	}
	visit(value)
	return result
}
