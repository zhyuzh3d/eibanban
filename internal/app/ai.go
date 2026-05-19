package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func callResponses(ctx context.Context, cfg Config, history []Message) (Message, error) {
	if cfg.APIKey == "" {
		return Message{}, errors.New("missing OPENAI_API_KEY in ~/.codex/auth.json or environment")
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		return Message{}, errors.New("base URL and model are required")
	}

	input, err := buildInput(history)
	if err != nil {
		return Message{}, err
	}
	body := map[string]any{
		"model":  cfg.Model,
		"input":  input,
		"stream": true,
		"tools": []map[string]any{
			{"type": "image_generation"},
		},
	}
	if wantsImage(history) {
		body["tool_choice"] = map[string]any{"type": "image_generation"}
	}
	data, _ := json.Marshal(body)

	url := strings.TrimRight(cfg.BaseURL, "/")
	if !strings.HasSuffix(url, "/v1") {
		url += "/v1"
	}
	url += "/responses"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("api %s: %s", resp.Status, trimForStatus(raw))
	}

	text, images, err := parseResponseStream(resp.Body)
	if err != nil {
		return Message{}, err
	}
	return Message{Role: "assistant", Text: text, Images: images, CreatedAt: time.Now()}, nil
}

func buildInput(history []Message) ([]map[string]any, error) {
	mem, _ := os.ReadFile("memories.md")
	char, _ := os.ReadFile("character.md")
	systemText := strings.TrimSpace("# character.md\n" + string(char) + "\n\n# memories.md\n" + string(mem))
	input := []map[string]any{{
		"role": "system",
		"content": []map[string]any{{
			"type": "input_text",
			"text": systemText,
		}},
	}}
	start := 0
	if len(history) > 20 {
		start = len(history) - 20
	}
	for _, m := range history[start:] {
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		parts := []map[string]any{}
		if strings.TrimSpace(m.Text) != "" {
			typ := "input_text"
			if role == "assistant" {
				typ = "output_text"
			}
			parts = append(parts, map[string]any{"type": typ, "text": m.Text})
		}
		for _, img := range m.Attachments {
			dataURL, err := fileDataURL(img)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{"type": "input_image", "image_url": dataURL})
		}
		if len(parts) == 0 {
			continue
		}
		input = append(input, map[string]any{"role": role, "content": parts})
	}
	return input, nil
}

func parseResponse(raw []byte) (string, []string, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", nil, err
	}
	var texts []string
	var images []string
	walkJSON(root, func(m map[string]any) {
		if t, _ := m["type"].(string); t == "output_text" || t == "text" {
			if s, _ := m["text"].(string); s != "" {
				texts = append(texts, s)
			}
		}
		for _, key := range []string{"result", "b64_json", "image_base64"} {
			if s, _ := m[key].(string); looksBase64Image(s) {
				if path, err := saveBase64Image(s); err == nil {
					images = append(images, path)
				}
			}
		}
		for _, key := range []string{"image_url", "url"} {
			if s, _ := m[key].(string); strings.HasPrefix(s, "data:image/") {
				if path, err := saveDataURL(s); err == nil {
					images = append(images, path)
				}
			}
		}
	})
	if len(texts) == 0 {
		if s := extractOutputText(root); s != "" {
			texts = append(texts, s)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n")), dedupeStrings(images), nil
}

func parseResponseStream(r io.Reader) (string, []string, error) {
	br := bufio.NewReaderSize(r, 1024*1024)
	var eventName string
	var data strings.Builder
	var lastText string
	var lastImages []string
	var sawCompleted bool

	flush := func() error {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" || payload == "[DONE]" {
			eventName = ""
			return nil
		}

		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			eventName = ""
			return nil
		}
		typ, _ := evt["type"].(string)
		if typ == "" {
			typ = eventName
		}
		if typ == "response.failed" || typ == "response.incomplete" {
			if errObj, ok := evt["error"]; ok {
				return fmt.Errorf("api stream failed: %v", errObj)
			}
			return fmt.Errorf("api stream failed: %s", typ)
		}

		if typ == "response.output_item.done" || typ == "response.completed" {
			text, images, err := parseResponse([]byte(payload))
			if err == nil {
				if text != "" {
					lastText = text
				}
				if len(images) > 0 {
					lastImages = append(lastImages, images...)
				}
			}
		}
		if typ == "response.completed" {
			sawCompleted = true
			if response, ok := evt["response"]; ok {
				raw, _ := json.Marshal(response)
				text, images, err := parseResponse(raw)
				if err == nil {
					if text != "" {
						lastText = text
					}
					if len(images) > 0 {
						lastImages = append(lastImages, images...)
					}
				}
			}
		}
		eventName = ""
		return nil
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if err := flush(); err != nil {
					return "", nil, err
				}
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if data.Len() > 0 {
					if err := flush(); err != nil {
						return "", nil, err
					}
				}
				break
			}
			return "", nil, err
		}
	}

	if !sawCompleted && lastText == "" && len(lastImages) == 0 {
		return "", nil, errors.New("stream ended without a completed response")
	}
	return lastText, dedupeStrings(lastImages), nil
}

func walkJSON(v any, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, child := range x {
			walkJSON(child, fn)
		}
	case []any:
		for _, child := range x {
			walkJSON(child, fn)
		}
	}
}

func extractOutputText(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if s, _ := m["output_text"].(string); s != "" {
		return s
	}
	return ""
}

func saveBase64Image(s string) (string, error) {
	ext := ".png"
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	if len(data) > 3 && data[0] == 0xff && data[1] == 0xd8 {
		ext = ".jpg"
	} else if len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		ext = ".webp"
	}
	sum := sha1.Sum(data)
	path := filepath.Join("app_outputs", "images", "response_"+hex.EncodeToString(sum[:8])+ext)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0644)
}

func saveDataURL(s string) (string, error) {
	idx := strings.Index(s, ",")
	if idx < 0 {
		return "", errors.New("bad data URL")
	}
	meta := s[:idx]
	ext := ".png"
	if strings.Contains(meta, "jpeg") || strings.Contains(meta, "jpg") {
		ext = ".jpg"
	} else if strings.Contains(meta, "webp") {
		ext = ".webp"
	}
	data, err := base64.StdEncoding.DecodeString(s[idx+1:])
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(data)
	path := filepath.Join("app_outputs", "images", "response_"+hex.EncodeToString(sum[:8])+ext)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0644)
}
