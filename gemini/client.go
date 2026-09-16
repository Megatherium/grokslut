// Package gemini reads a user's Gemini history through the private batched RPCs
// used by gemini.google.com. It is read-only and deliberately preserves raw
// positional payloads because Google may change their undocumented shape.
package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/grok"
)

const (
	listRPC              = "MaZiqc"
	loadRPC              = "hNvQHb"
	conversationPageSize = 100
)

var ErrAuthExpired = grok.ErrAuthExpired

type Client struct {
	BaseURL *url.URL
	HTTP    *http.Client
	Headers http.Header

	mu     sync.Mutex
	tokens pageTokens
	titles map[string]grok.ConversationSummary
	reqid  atomic.Uint64
}

type pageTokens struct {
	At, Build, SID string
}

func NewClient(session auth.Session, baseURL string) (*Client, error) {
	if baseURL == "" {
		baseURL = "https://gemini.google.com"
	}
	base, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" {
		return nil, fmt.Errorf("base URL must be HTTP(S) with a host")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Jar: jar, Timeout: 90 * time.Second}
	if err := session.Apply(httpClient, base.String()); err != nil {
		return nil, err
	}
	headers := make(http.Header)
	for name, value := range session.Headers {
		if strings.EqualFold(name, "user-agent") || strings.EqualFold(name, "accept-language") {
			headers.Set(name, value)
		}
	}
	client := &Client{BaseURL: base, HTTP: httpClient, Headers: headers, titles: map[string]grok.ConversationSummary{}}
	client.reqid.Store(uint64(time.Now().UnixNano() % 900000))
	return client, nil
}

func (c *Client) Verify() error {
	_, err := c.ListConversations(1, "")
	return err
}

func (c *Client) ListConversations(pageSize int, cursor string) (grok.ListResult, error) {
	return c.listConversations(pageSize, cursor, false)
}

func (c *Client) listConversations(pageSize int, cursor string, pinned bool) (grok.ListResult, error) {
	if pageSize < 1 {
		pageSize = 36
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var cursorValue any
	if cursor != "" {
		cursorValue = cursor
	}
	flag := 0
	if pinned {
		flag = 1
	}
	raw, err := c.call(listRPC, []any{pageSize, cursorValue, []any{flag, nil, 1}}, "/")
	if err != nil {
		return grok.ListResult{}, err
	}
	result, err := decodeConversationList(raw)
	if err != nil {
		return grok.ListResult{}, err
	}
	for index := range result.Conversations {
		result.Conversations[index].Provider = "gemini"
		result.Conversations[index].ProjectID = "gemini"
		result.Conversations[index].ProjectTitle = "Gemini"
		c.mu.Lock()
		c.titles[result.Conversations[index].ID] = result.Conversations[index]
		c.mu.Unlock()
	}
	return result, nil
}

func (c *Client) ListAllConversations(pageSize int) ([]grok.ConversationSummary, error) {
	seen := map[string]bool{}
	var all []grok.ConversationSummary
	appendUnique := func(values []grok.ConversationSummary) {
		for _, value := range values {
			if value.ID != "" && !seen[value.ID] {
				seen[value.ID] = true
				all = append(all, value)
			}
		}
	}
	walk := func(pinned bool) error {
		cursor := ""
		seenCursors := map[string]bool{}
		for page := 0; page < 1000; page++ {
			result, err := c.listConversations(pageSize, cursor, pinned)
			if err != nil {
				return err
			}
			appendUnique(result.Conversations)
			if result.NextCursor == "" {
				return nil
			}
			if result.NextCursor == cursor || seenCursors[result.NextCursor] {
				return errors.New("Gemini returned a repeated conversation-list cursor")
			}
			seenCursors[result.NextCursor] = true
			cursor = result.NextCursor
		}
		return errors.New("Gemini conversation-list pagination exceeded 1000 pages")
	}
	if err := walk(true); err != nil {
		return nil, err
	}
	if err := walk(false); err != nil {
		return nil, err
	}
	return all, nil
}

func (c *Client) LoadConversation(id string) (grok.Thread, error) {
	return c.LoadConversationProgress(id, nil)
}

func (c *Client) LoadConversationProgress(id string, progress grok.LoadProgress) (grok.Thread, error) {
	if id == "" {
		return grok.Thread{}, errors.New("conversation id is required")
	}
	c.mu.Lock()
	conversation, found := c.titles[id]
	c.mu.Unlock()
	if !found {
		conversation = grok.ConversationSummary{Provider: "gemini", ID: id, Title: "Gemini conversation " + id, ProjectID: "gemini", ProjectTitle: "Gemini"}
	}
	thread := grok.Thread{Conversation: conversation}
	var pages []json.RawMessage
	cursor := ""
	seenCursors := map[string]bool{}
	loaded := 0
	if progress != nil {
		progress(0, conversationPageSize*2)
	}
	for page := 0; page < 10000; page++ {
		var cursorValue any
		if cursor != "" {
			cursorValue = cursor
		}
		raw, err := c.call(loadRPC, []any{id, conversationPageSize, cursorValue, 1, []any{1}, []any{4}, nil, 1}, "/app/"+url.PathEscape(id))
		if err != nil {
			return grok.Thread{}, err
		}
		pages = append(pages, raw)
		responses, nextCursor, err := decodeConversationPage(raw, id, page)
		if err != nil {
			return grok.Thread{}, err
		}
		thread.Responses = append(thread.Responses, responses...)
		loaded += len(responses)
		total := loaded
		if nextCursor != "" {
			total += conversationPageSize * 2
		}
		if progress != nil {
			progress(loaded, total)
		}
		if nextCursor == "" {
			thread.Nodes, _ = json.Marshal(pages)
			return thread, nil
		}
		if nextCursor == cursor || seenCursors[nextCursor] {
			return grok.Thread{}, errors.New("Gemini returned a repeated conversation cursor")
		}
		seenCursors[nextCursor] = true
		cursor = nextCursor
	}
	return grok.Thread{}, errors.New("Gemini conversation pagination exceeded 10000 pages")
}

func (c *Client) GetMedia(assetURL string) (*http.Response, error) {
	asset, err := url.Parse(assetURL)
	if err != nil || (asset.Scheme != "http" && asset.Scheme != "https") || asset.Hostname() == "" {
		return nil, errors.New("invalid media URL")
	}
	req, err := http.NewRequest(http.MethodGet, asset.String(), nil)
	if err != nil {
		return nil, err
	}
	for name, values := range c.Headers {
		req.Header[name] = append([]string(nil), values...)
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
		return nil, &grok.HTTPError{Status: response.StatusCode}
	}
	return response, nil
}

func (c *Client) call(rpc string, args any, sourcePath string) (json.RawMessage, error) {
	tokens, err := c.pageTokens()
	if err != nil {
		return nil, err
	}
	inner, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	envelope, err := json.Marshal([]any{[]any{[]any{rpc, string(inner), nil, "generic"}}})
	if err != nil {
		return nil, err
	}
	query := url.Values{
		"rpcids":      {rpc},
		"source-path": {sourcePath},
		"bl":          {tokens.Build},
		"f.sid":       {tokens.SID},
		"hl":          {"en"},
		"_reqid":      {strconv.FormatUint(c.reqid.Add(1), 10)},
		"rt":          {"c"},
	}
	endpoint := *c.BaseURL
	endpoint.Path = "/_/BardChatUi/data/batchexecute"
	endpoint.RawQuery = query.Encode()
	form := url.Values{"f.req": {string(envelope)}, "at": {tokens.At}}
	req, err := http.NewRequest(http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	for name, values := range c.Headers {
		req.Header[name] = append([]string(nil), values...)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Origin", c.BaseURL.Scheme+"://"+c.BaseURL.Host)
	req.Header.Set("Referer", c.BaseURL.ResolveReference(&url.URL{Path: sourcePath}).String())
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrAuthExpired
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &grok.HTTPError{Status: response.StatusCode, Body: string(body[:min(len(body), 500)])}
	}
	payload, err := decodeBatchResponse(body, rpc)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *Client) pageTokens() (pageTokens, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens.At != "" && c.tokens.Build != "" && c.tokens.SID != "" {
		return c.tokens, nil
	}
	endpoint := c.BaseURL.ResolveReference(&url.URL{Path: "/app"})
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return pageTokens{}, err
	}
	for name, values := range c.Headers {
		req.Header[name] = append([]string(nil), values...)
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return pageTokens{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return pageTokens{}, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return pageTokens{}, ErrAuthExpired
	}
	tokens := pageTokens{At: pageValue(body, "SNlM0e"), Build: pageValue(body, "cfb2h"), SID: pageValue(body, "FdrFJe")}
	if tokens.At == "" || tokens.Build == "" || tokens.SID == "" {
		return pageTokens{}, ErrAuthExpired
	}
	c.tokens = tokens
	return tokens, nil
}

func pageValue(body []byte, key string) string {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `":"((?:\\.|[^"\\])*)"`)
	match := pattern.FindSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	value, err := strconv.Unquote(`"` + string(match[1]) + `"`)
	if err != nil {
		return ""
	}
	return value
}

func decodeBatchResponse(body []byte, rpc string) (json.RawMessage, error) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[[") {
			continue
		}
		var outer []json.RawMessage
		if json.Unmarshal([]byte(line), &outer) != nil {
			continue
		}
		for _, raw := range outer {
			var record []json.RawMessage
			if json.Unmarshal(raw, &record) != nil || len(record) < 3 || rawString(record[1]) != rpc {
				continue
			}
			var encoded string
			if json.Unmarshal(record[2], &encoded) != nil {
				continue
			}
			if json.Valid([]byte(encoded)) {
				return json.RawMessage(encoded), nil
			}
		}
	}
	return nil, fmt.Errorf("Gemini %s response did not contain a valid payload", rpc)
}

func decodeConversationList(raw json.RawMessage) (grok.ListResult, error) {
	var payload []json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload) < 3 {
		return grok.ListResult{}, errors.New("invalid Gemini conversation list payload")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(payload[2], &entries); err != nil {
		return grok.ListResult{}, errors.New("invalid Gemini conversation entries")
	}
	result := grok.ListResult{NextCursor: rawString(payload[1]), Raw: raw}
	for _, entry := range entries {
		var fields []json.RawMessage
		if json.Unmarshal(entry, &fields) != nil {
			continue
		}
		id, title := stringIndex(fields, 0), stringIndex(fields, 1)
		if id == "" {
			continue
		}
		result.Conversations = append(result.Conversations, grok.ConversationSummary{
			Provider:  "gemini",
			ID:        id,
			Title:     fallback(title, "Untitled Gemini conversation"),
			UpdatedAt: timestampIndex(fields, 5),
			Raw:       entry,
		})
	}
	return result, nil
}

func decodeConversationPage(raw json.RawMessage, conversationID string, page int) ([]json.RawMessage, string, error) {
	var payload []json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload) < 2 {
		return nil, "", errors.New("invalid Gemini conversation payload")
	}
	var turns []json.RawMessage
	if err := json.Unmarshal(payload[0], &turns); err != nil {
		return nil, "", errors.New("invalid Gemini turn list")
	}
	var responses []json.RawMessage
	for index, rawTurn := range turns {
		var turn []json.RawMessage
		if json.Unmarshal(rawTurn, &turn) != nil {
			continue
		}
		humanID := nestedString(turn, 0, 1)
		if humanID == "" {
			humanID = fmt.Sprintf("%s-human-%d-%d", conversationID, page, index)
		}
		parentID := nestedString(turn, 1, 2)
		createdAt := timestampIndex(turn, 4)
		humanText := nestedString(turn, 2, 0, 0)
		attachments, assetURLs := turnAttachments(turn)
		for _, attachment := range attachments {
			humanText += "\n\n[Attachment: " + attachment.Name + "](" + attachment.URL + ")"
		}
		human := map[string]any{
			"provider":         "gemini",
			"responseId":       humanID,
			"parentResponseId": parentID,
			"sender":           "human",
			"message":          humanText,
			"createTime":       createdAt,
			"geminiTurn":       rawTurn,
		}
		if len(attachments) > 0 {
			human["fileAttachments"] = attachments
			human["assetUrls"] = assetURLs
		}
		if encoded, err := json.Marshal(human); err == nil {
			responses = append(responses, encoded)
		}
		assistantID := nestedString(turn, 3, 0, 0, 0)
		assistantText := nestedStrings(turn, 3, 0, 0, 1)
		if assistantID == "" && assistantText == "" {
			continue
		}
		if assistantID == "" {
			assistantID = fmt.Sprintf("%s-assistant-%d-%d", conversationID, page, index)
		}
		assistant := map[string]any{
			"provider":         "gemini",
			"responseId":       assistantID,
			"parentResponseId": humanID,
			"sender":           "assistant",
			"message":          assistantText,
			"createTime":       createdAt,
		}
		if encoded, err := json.Marshal(assistant); err == nil {
			responses = append(responses, encoded)
		}
	}
	return responses, rawString(payload[1]), nil
}

type attachment struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType,omitempty"`
	URL      string `json:"url"`
}

func turnAttachments(turn []json.RawMessage) ([]attachment, []string) {
	raw := nestedRaw(turn, 2, 0, 4, 0, 4)
	var records []json.RawMessage
	if json.Unmarshal(raw, &records) != nil {
		return nil, nil
	}
	var attachments []attachment
	var urls []string
	for _, rawRecord := range records {
		var record []json.RawMessage
		if json.Unmarshal(rawRecord, &record) != nil {
			continue
		}
		var candidates []string
		if len(record) > 7 {
			_ = json.Unmarshal(record[7], &candidates)
		}
		assetURL := ""
		if len(candidates) > 1 {
			assetURL = candidates[1]
		} else if len(candidates) == 1 {
			assetURL = candidates[0]
		}
		if !strings.HasPrefix(assetURL, "http") {
			continue
		}
		name := stringIndex(record, 2)
		if name == "" {
			name = "Gemini attachment"
		}
		attachments = append(attachments, attachment{Name: name, MimeType: stringIndex(record, 11), URL: assetURL})
		urls = append(urls, assetURL)
	}
	return attachments, urls
}

func nestedString(values []json.RawMessage, indexes ...int) string {
	current := nestedRaw(values, indexes...)
	return rawString(current)
}

func nestedRaw(values []json.RawMessage, indexes ...int) json.RawMessage {
	current, _ := json.Marshal(values)
	for _, index := range indexes {
		var array []json.RawMessage
		if json.Unmarshal(current, &array) != nil || index < 0 || index >= len(array) {
			return nil
		}
		current = array[index]
	}
	return current
}

func nestedStrings(values []json.RawMessage, indexes ...int) string {
	var current json.RawMessage
	current, _ = json.Marshal(values)
	for _, index := range indexes {
		var array []json.RawMessage
		if json.Unmarshal(current, &array) != nil || index < 0 || index >= len(array) {
			return ""
		}
		current = array[index]
	}
	var stringsValue []string
	if json.Unmarshal(current, &stringsValue) == nil {
		return strings.Join(stringsValue, "\n")
	}
	return rawString(current)
}

func stringIndex(values []json.RawMessage, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return rawString(values[index])
}

func timestampIndex(values []json.RawMessage, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	var timestamp []int64
	if json.Unmarshal(values[index], &timestamp) != nil || len(timestamp) == 0 {
		return ""
	}
	nanoseconds := int64(0)
	if len(timestamp) > 1 {
		nanoseconds = timestamp[1]
	}
	return time.Unix(timestamp[0], nanoseconds).UTC().Format(time.RFC3339Nano)
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
