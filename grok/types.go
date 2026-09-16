package grok

import "encoding/json"

type ConversationSummary struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	CreatedAt    string          `json:"createdAt,omitempty"`
	UpdatedAt    string          `json:"updatedAt,omitempty"`
	Preview      string          `json:"preview,omitempty"`
	ProjectID    string          `json:"projectId,omitempty"`
	ProjectTitle string          `json:"projectTitle,omitempty"`
	Raw          json.RawMessage `json:"-"`
}

type ListResult struct {
	Conversations []ConversationSummary `json:"conversations"`
	NextCursor    string                `json:"nextCursor"`
	Raw           json.RawMessage       `json:"-"`
}

type Thread struct {
	Conversation ConversationSummary
	Responses    []json.RawMessage
	Nodes        json.RawMessage
}
