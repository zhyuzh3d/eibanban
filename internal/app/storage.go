package app

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func historyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "chengcheng-chat", "chat.db")
}

func loadHistory(path string) ([]Message, error) {
	db, err := openHistoryDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, thread_id, role, content, created_at
		FROM messages
		WHERE thread_id = ? AND deleted_at IS NULL
		ORDER BY created_at, seq
	`, defaultThreadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Role, &m.Text, &created); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if m.CreatedAt.IsZero() {
			m.CreatedAt = time.Now()
		}
		attachments, images, err := loadMessageAttachments(db, m.ID)
		if err != nil {
			return nil, err
		}
		m.Attachments = attachments
		m.Images = images
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (a *ChatApp) saveHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saveHistoryLocked(false)
}

func (a *ChatApp) saveHistoryAllowEmpty() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saveHistoryLocked(true)
}

func (a *ChatApp) saveHistoryLocked(allowEmpty bool) {
	if a.historyPath == "" {
		return
	}
	if len(a.messages) == 0 && !allowEmpty {
		return
	}
	if err := saveHistoryDB(a.historyPath, a.messages, allowEmpty); err != nil {
		a.status = "History save failed: " + err.Error()
	}
}

func openHistoryDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
}

func initHistoryDB(path string) error {
	db, err := openHistoryDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			provider TEXT,
			model TEXT,
			instructions_path TEXT,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			user_id TEXT,
			default_agent_id TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT,
			FOREIGN KEY(user_id) REFERENCES users(id),
			FOREIGN KEY(default_agent_id) REFERENCES agents(id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			seq INTEGER,
			role TEXT NOT NULL,
			actor_type TEXT NOT NULL DEFAULT 'user',
			actor_id TEXT,
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'complete',
			model TEXT,
			parent_message_id TEXT,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT,
			FOREIGN KEY(thread_id) REFERENCES threads(id),
			FOREIGN KEY(parent_message_id) REFERENCES messages(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_thread_seq ON messages(thread_id, seq)`,
		`CREATE TABLE IF NOT EXISTS attachments (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'attachment',
			path TEXT,
			mime_type TEXT,
			display_name TEXT,
			size_bytes INTEGER,
			width INTEGER,
			height INTEGER,
			content_id TEXT,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			FOREIGN KEY(message_id) REFERENCES messages(id),
			FOREIGN KEY(thread_id) REFERENCES threads(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_attachments_message ON attachments(message_id)`,
		`CREATE TABLE IF NOT EXISTS tool_calls (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			agent_id TEXT,
			name TEXT NOT NULL,
			arguments_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			started_at TEXT,
			completed_at TEXT,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			FOREIGN KEY(message_id) REFERENCES messages(id),
			FOREIGN KEY(thread_id) REFERENCES threads(id),
			FOREIGN KEY(agent_id) REFERENCES agents(id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT OR IGNORE INTO users(id, display_name, created_at) VALUES(?, ?, ?)`, defaultUserID, "Local User", now); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO agents(id, display_name, provider, created_at) VALUES(?, ?, ?, ?)`, defaultAgentID, "Assistant", "OpenAI-compatible", now); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO threads(id, title, user_id, default_agent_id, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?)`, defaultThreadID, "Default conversation", defaultUserID, defaultAgentID, now, now)
	return err
}

func loadMessageAttachments(db *sql.DB, messageID string) ([]string, []string, error) {
	rows, err := db.Query(`
		SELECT kind, path
		FROM attachments
		WHERE message_id = ?
		ORDER BY created_at, id
	`, messageID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var attachments, images []string
	for rows.Next() {
		var kind, path string
		if err := rows.Scan(&kind, &path); err != nil {
			return nil, nil, err
		}
		switch kind {
		case "assistant_image", "generated_image", "output_image":
			images = append(images, path)
		default:
			attachments = append(attachments, path)
		}
	}
	return attachments, images, rows.Err()
}

func saveHistoryDB(path string, messages []Message, allowEmpty bool) error {
	if len(messages) == 0 && !allowEmpty {
		return nil
	}
	if err := initHistoryDB(path); err != nil {
		return err
	}
	db, err := openHistoryDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Format(time.RFC3339Nano)
	if allowEmpty && len(messages) == 0 {
		if _, err := tx.Exec(`UPDATE messages SET deleted_at = ? WHERE thread_id = ? AND deleted_at IS NULL`, now, defaultThreadID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE threads SET updated_at = ? WHERE id = ?`, now, defaultThreadID); err != nil {
			return err
		}
		return tx.Commit()
	}
	for i := range messages {
		m := &messages[i]
		if m.ID == "" {
			m.ID = newID("msg")
		}
		if m.ThreadID == "" {
			m.ThreadID = defaultThreadID
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = time.Now()
		}
		actorType := "user"
		actorID := defaultUserID
		if m.Role == "assistant" {
			actorType = "agent"
			actorID = defaultAgentID
		}
		created := m.CreatedAt.Format(time.RFC3339Nano)
		if _, err := tx.Exec(`
			INSERT INTO messages(id, thread_id, seq, role, actor_type, actor_id, content, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				seq = excluded.seq,
				role = excluded.role,
				actor_type = excluded.actor_type,
				actor_id = excluded.actor_id,
				content = excluded.content,
				updated_at = excluded.updated_at,
				deleted_at = NULL
		`, m.ID, m.ThreadID, i+1, m.Role, actorType, actorID, m.Text, created, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM attachments WHERE message_id = ?`, m.ID); err != nil {
			return err
		}
		for idx, p := range m.Attachments {
			if err := insertAttachment(tx, m, idx, "user_attachment", p); err != nil {
				return err
			}
		}
		for idx, p := range m.Images {
			if err := insertAttachment(tx, m, idx, "assistant_image", p); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE threads SET updated_at = ? WHERE id = ?`, now, defaultThreadID); err != nil {
		return err
	}
	return tx.Commit()
}

func insertAttachment(tx *sql.Tx, m *Message, idx int, kind, path string) error {
	info, _ := os.Stat(path)
	var size int64
	if info != nil {
		size = info.Size()
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err := tx.Exec(`
		INSERT INTO attachments(id, message_id, thread_id, kind, path, mime_type, display_name, size_bytes, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, newID(fmt.Sprintf("att_%d", idx)), m.ID, m.ThreadID, kind, path, mimeType, filepath.Base(path), size, now)
	return err
}

func migrateJSONHistory(dbPath string) error {
	jsonPath := filepath.Join(filepath.Dir(dbPath), "history.json")
	if _, err := os.Stat(jsonPath); err != nil {
		return nil
	}
	dbMessages, err := loadHistory(dbPath)
	if err == nil && len(dbMessages) > 0 {
		return nil
	}
	msgs, err := loadJSONHistory(jsonPath)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	if err := saveHistoryDB(dbPath, msgs, false); err != nil {
		return err
	}
	_ = os.Rename(jsonPath, jsonPath+".migrated")
	return nil
}

func loadJSONHistory(path string) ([]Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var store historyStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Messages == nil {
		if backup, err := loadJSONHistory(path + ".bak"); err == nil {
			return backup, nil
		}
		return []Message{}, nil
	}
	for i := range store.Messages {
		if store.Messages[i].ID == "" {
			store.Messages[i].ID = newID("jsonmsg")
		}
		if store.Messages[i].ThreadID == "" {
			store.Messages[i].ThreadID = defaultThreadID
		}
	}
	return store.Messages, nil
}

func newID(prefix string) string {
	now := time.Now().UnixNano()
	sum := sha1.Sum([]byte(fmt.Sprintf("%s-%d-%d", prefix, now, time.Now().Nanosecond())))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}
