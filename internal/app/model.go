package app

import (
	"time"

	"gioui.org/f32"
)

type Config struct {
	BaseURL string
	Model   string
	APIKey  string
}

type Message struct {
	ID          string
	ThreadID    string
	Role        string
	Text        string
	Images      []string
	CreatedAt   time.Time
	Attachments []string
}

type previewState struct {
	tag      struct{}
	path     string
	zoom     float32
	mode     string
	offset   f32.Point
	dragging bool
	lastPos  f32.Point
}

type historyStore struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []Message `json:"messages"`
}

const (
	defaultUserID   = "local-user"
	defaultAgentID  = "assistant"
	defaultThreadID = "default-thread"
)
