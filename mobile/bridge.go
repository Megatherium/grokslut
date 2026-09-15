// Package mobile is a small JSON-only façade suitable for gomobile binding.
// It has no Android dependency and is intentionally usable by future native UI.
package mobile

import (
	"encoding/json"
	"sync"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/exporter"
	"github.com/Megatherium/grokslut/grok"
)

var state struct {
	sync.RWMutex
	client *grok.Client
}

// SessionFromCookies accepts the same private JSON envelope as the CLI.
func SessionFromCookies(cookiesJSON string) error {
	session, err := auth.FromJSON([]byte(cookiesJSON))
	if err != nil {
		return err
	}
	client, err := grok.NewClient(session, "https://grok.com")
	if err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	state.client = client
	return nil
}

func SessionClear()        { state.Lock(); defer state.Unlock(); state.client = nil }
func SessionIsValid() bool { state.RLock(); defer state.RUnlock(); return state.client != nil }

func ListConversations(pageSize int, cursor string) (string, string, error) {
	client, err := currentClient()
	if err != nil {
		return "", "", err
	}
	result, err := client.ListConversations(pageSize, cursor)
	if err != nil {
		return "", "", err
	}
	data, err := json.Marshal(result.Conversations)
	return string(data), result.NextCursor, err
}

func LoadConversation(id string) (string, error) {
	client, err := currentClient()
	if err != nil {
		return "", err
	}
	thread, err := client.LoadConversation(id)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Conversation json.RawMessage   `json:"conversation"`
		Responses    []json.RawMessage `json:"responses"`
	}{thread.Conversation.Raw, thread.Responses})
	return string(data), err
}

func ExportConversations(idsJSON, format, outDir string) (string, error) {
	var ids []string
	if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
		return "", err
	}
	client, err := currentClient()
	if err != nil {
		return "", err
	}
	result, err := (exporter.Exporter{Client: client}).Export(ids, exporter.Format(format), outDir, nil)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}

func currentClient() (*grok.Client, error) {
	state.RLock()
	defer state.RUnlock()
	if state.client == nil {
		return nil, grok.ErrAuthExpired
	}
	return state.client, nil
}
