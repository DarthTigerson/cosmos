// Package llm talks to OpenAI-compatible model servers (llama.cpp's
// llama-server in practice). Every structured call sends a JSON schema so
// small models are held to the shape instead of trusted to follow it.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"cosmos/core/internal/config"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Tier        string // main, worker, sleeper
	Role        string // route, notice, writer, ... (a label for logs and tests)
	System      string
	Messages    []Message
	Schema      any // when set, the reply must be JSON of this shape (a json.RawMessage keeps field order)
	MaxTokens   int
	Temperature float64
	// OnText, when set, is called with the reply so far as it is
	// written, so a listener can show it word by word.
	OnText func(soFar string)
}

type Response struct {
	Text             string
	PromptTokens     int
	CompletionTokens int
	Duration         time.Duration
}

type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// HTTP is the real client. Prepare, when set, is called before each request
// so a launcher can start the server for that tier.
type HTTP struct {
	Cfg     *config.Holder
	Prepare func(ctx context.Context, tier string) error
	client  http.Client
}

func NewHTTP(cfg *config.Holder) *HTTP {
	return &HTTP{Cfg: cfg, client: http.Client{Timeout: 10 * time.Minute}}
}

func (h *HTTP) Complete(ctx context.Context, req Request) (Response, error) {
	if h.Prepare != nil {
		if err := h.Prepare(ctx, req.Tier); err != nil {
			return Response{}, err
		}
	}
	ep := h.Cfg.Get().Tier(req.Tier)
	if ep.BaseURL == "" {
		return Response{}, errors.New("no model endpoint configured")
	}
	msgs := make([]Message, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, req.Messages...)
	body := map[string]any{
		"model":       ep.Model,
		"messages":    msgs,
		"stream":      req.OnText != nil,
		"temperature": req.Temperature,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	// Thinking models (Qwen3 and later) otherwise spend the whole token
	// budget reasoning and return an empty answer. Every call here is a
	// small, bounded job, so the thinking phase is switched off. Chat
	// templates without this switch ignore it.
	body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	if req.Schema != nil {
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": req.Role, "strict": true, "schema": req.Schema},
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ep.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if ep.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+ep.APIKey)
	}
	start := time.Now()
	res, err := h.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("model server (%s) unreachable: %w", req.Tier, err)
	}
	defer res.Body.Close()
	if req.OnText != nil && res.StatusCode == http.StatusOK {
		text, err := readStream(res.Body, req.OnText)
		if err != nil {
			return Response{}, fmt.Errorf("model server (%s) stream broke: %w", req.Tier, err)
		}
		return Response{Text: StripThinking(text), Duration: time.Since(start)}, nil
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return Response{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("model server (%s) returned %d: %s", req.Tier, res.StatusCode, truncate(string(data), 300))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Response{}, fmt.Errorf("model server (%s) sent unreadable JSON: %w", req.Tier, err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("model server (%s) sent no choices", req.Tier)
	}
	return Response{
		Text:             StripThinking(parsed.Choices[0].Message.Content),
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		Duration:         time.Since(start),
	}, nil
}

// Embed turns texts into vectors with the embedding model. It reports an
// error when no such model is set up.
func (h *HTTP) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	ep := h.Cfg.Get().Embed
	if ep.BaseURL == "" {
		return nil, errors.New("no embedding model configured")
	}
	if h.Prepare != nil {
		if err := h.Prepare(ctx, "embed"); err != nil {
			return nil, err
		}
	}
	raw, _ := json.Marshal(map[string]any{"model": ep.Model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ep.BaseURL, "/")+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding model unreachable: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding model returned %d: %s", res.StatusCode, truncate(string(data), 200))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	for _, v := range out {
		if len(v) == 0 {
			return nil, errors.New("embedding model left a text out")
		}
	}
	return out, nil
}

// readStream reads a reply sent as server-sent events: one "data:" line
// per piece of text, ending with "data: [DONE]".
func readStream(body io.Reader, onText func(string)) (string, error) {
	var sb strings.Builder
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		payload, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		if piece := chunk.Choices[0].Delta.Content; piece != "" {
			sb.WriteString(piece)
			onText(sb.String())
		}
	}
	return sb.String(), sc.Err()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// StripThinking removes a leading <think>…</think> block that reasoning
// models put in front of the answer.
func StripThinking(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "<think>") {
		_, rest, ok := strings.Cut(s, "</think>")
		if !ok {
			return ""
		}
		s = strings.TrimSpace(rest)
	}
	return s
}

// ParseJSON reads the first JSON object in text into v. Local models
// sometimes wrap JSON in prose or code fences even under a schema.
func ParseJSON(text string, v any) error {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end < start {
		return fmt.Errorf("no JSON object in model reply: %s", truncate(text, 120))
	}
	return json.Unmarshal([]byte(text[start:end+1]), v)
}

// Fake is a scripted client for tests. Handlers are keyed by Request.Role.
type Fake struct {
	mu       sync.Mutex
	Handlers map[string]func(Request) (string, error)
	Calls    []Request
}

func (f *Fake) Complete(_ context.Context, req Request) (Response, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, req)
	h := f.Handlers[req.Role]
	f.mu.Unlock()
	if h == nil {
		return Response{}, fmt.Errorf("fake llm: no handler for role %q", req.Role)
	}
	text, err := h(req)
	return Response{Text: text}, err
}

// CallsFor returns how many requests a role received.
func (f *Fake) CallsFor(role string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if c.Role == role {
			n++
		}
	}
	return n
}
