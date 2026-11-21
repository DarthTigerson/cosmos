// Package mcp lets her use tools from Model Context Protocol servers the
// user runs on this computer: local programs spoken to over their
// standard input and output, one JSON message per line.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server is one MCP server as the user set it up.
type Server struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	// AllowChanges lets her use this server's tools that change things.
	// Without it she only gets the ones that read.
	AllowChanges bool `json:"allow_changes"`
}

// Tool is one tool a server offers.
type Tool struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"-"`
	ReadOnly    bool            `json:"read_only"`
}

// ID is the name the planner uses for a tool: "mcp.notes.search_notes".
func (t Tool) ID() string { return "mcp." + t.Server + "." + t.Name }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// client is one running server.
type client struct {
	cfg   Server
	cmd   *exec.Cmd
	in    io.WriteCloser
	mu    sync.Mutex
	next  int64
	wait  map[int64]chan message
	tools []Tool
	err   string
	dead  bool
}

func start(ctx context.Context, cfg Server) (*client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("no command set")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start it: %w", err)
	}
	c := &client{cfg: cfg, cmd: cmd, in: in, wait: map[int64]chan message{}}
	go c.read(out)

	var hello struct{}
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cosmos", "version": "1"},
	}, &hello); err != nil {
		c.stop()
		return nil, fmt.Errorf("it did not answer the greeting: %w", err)
	}
	c.send(message{JSONRPC: "2.0", Method: "notifications/initialized"})

	var listed struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
			Annotations struct {
				ReadOnlyHint *bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &listed); err != nil {
		c.stop()
		return nil, fmt.Errorf("it would not list its tools: %w", err)
	}
	for _, t := range listed.Tools {
		readOnly := looksReadOnly(t.Name)
		if t.Annotations.ReadOnlyHint != nil {
			readOnly = *t.Annotations.ReadOnlyHint
		}
		c.tools = append(c.tools, Tool{Server: cfg.Name, Name: t.Name, Description: strings.Join(strings.Fields(t.Description), " "), Schema: t.InputSchema, ReadOnly: readOnly})
	}
	return c, nil
}

// looksReadOnly guesses from a tool's name when the server does not say.
// Anything not plainly a lookup counts as changing something.
func looksReadOnly(name string) bool {
	n := strings.ToLower(name)
	for _, lead := range []string{"list", "read", "get", "search", "find", "show", "next", "fetch", "query", "outline", "doc_outline"} {
		if strings.HasPrefix(n, lead) {
			return true
		}
	}
	return false
}

func (c *client) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil || m.Method != "" {
			continue // a notification, a request to us, or noise
		}
		c.mu.Lock()
		ch := c.wait[*m.ID]
		delete(c.wait, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	c.mu.Lock()
	c.dead = true
	for id, ch := range c.wait {
		close(ch)
		delete(c.wait, id)
	}
	c.mu.Unlock()
}

func (c *client) send(m message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.in.Write(append(raw, '\n'))
	return err
}

func (c *client) call(ctx context.Context, method string, params, into any) error {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return errors.New("the server has stopped")
	}
	c.next++
	id := c.next
	ch := make(chan message, 1)
	c.wait[id] = ch
	c.mu.Unlock()
	if err := c.send(message{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return errors.New("the server stopped before answering")
		}
		if m.Error != nil {
			return errors.New(m.Error.Message)
		}
		if into != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, into)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
		return errors.New("it took too long to answer")
	}
}

func (c *client) stop() {
	c.in.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	go c.cmd.Wait()
}

// Manager keeps the user's servers running and knows their tools.
type Manager struct {
	mu      sync.Mutex
	clients map[string]*client // by name
	errs    map[string]string
}

func NewManager() *Manager {
	return &Manager{clients: map[string]*client{}, errs: map[string]string{}}
}

func same(a, b Server) bool {
	x, _ := json.Marshal(Server{Command: a.Command, Args: a.Args, Env: a.Env})
	y, _ := json.Marshal(Server{Command: b.Command, Args: b.Args, Env: b.Env})
	return string(x) == string(y)
}

// Sync starts the servers that are switched on and stops the rest. It is
// safe to call often; servers already running are left alone.
func (m *Manager) Sync(servers []Server) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]Server{}
	for _, s := range servers {
		if s.Enabled && s.Name != "" {
			want[s.Name] = s
		}
	}
	for name, c := range m.clients {
		c.mu.Lock()
		dead := c.dead
		c.mu.Unlock()
		if s, ok := want[name]; !ok || !same(s, c.cfg) || dead {
			c.stop()
			delete(m.clients, name)
		} else {
			c.cfg = s // the switches may have changed
		}
	}
	for name, s := range want {
		if _, ok := m.clients[name]; ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		c, err := start(ctx, s)
		cancel()
		if err != nil {
			m.errs[name] = err.Error()
			continue
		}
		delete(m.errs, name)
		m.clients[name] = c
	}
}

// Tools lists the tools she may use: every tool of a server allowed to
// change things, otherwise only the ones that read.
func (m *Manager) Tools() []Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Tool{}
	for _, c := range m.clients {
		for _, t := range c.tools {
			if t.ReadOnly || c.cfg.AllowChanges {
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Status says, for one server, how many tools it offers and what went
// wrong, if anything.
type Status struct {
	Running bool   `json:"running"`
	Error   string `json:"error"`
	Tools   []Tool `json:"tools"`
}

func (m *Manager) Status(name string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Error: m.errs[name], Tools: []Tool{}}
	if c, ok := m.clients[name]; ok {
		st.Running = true
		st.Tools = append(st.Tools, c.tools...)
	}
	return st
}

// Call runs one tool and returns what it said as text.
func (m *Manager) Call(ctx context.Context, id string, args json.RawMessage) (string, error) {
	rest, ok := strings.CutPrefix(id, "mcp.")
	server, tool, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 {
		return "", fmt.Errorf("%q is not a connected tool", id)
	}
	m.mu.Lock()
	c := m.clients[server]
	m.mu.Unlock()
	if c == nil {
		return "", fmt.Errorf("the %s server is not running", server)
	}
	allowed := false
	for _, t := range c.tools {
		if t.Name == tool && (t.ReadOnly || c.cfg.AllowChanges) {
			allowed = true
		}
	}
	if !allowed {
		return "", fmt.Errorf("%s is not a tool she may use", id)
	}
	var arguments any = map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return "", fmt.Errorf("unreadable arguments: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments}, &res); err != nil {
		return "", err
	}
	parts := []string{}
	for _, p := range res.Content {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			parts = append(parts, p.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if res.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// Stop shuts every server down.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, c := range m.clients {
		c.stop()
		delete(m.clients, name)
	}
}

// Shape turns a tool's own description of its arguments into the plain
// subset a small model's grammar can follow: objects, lists, text,
// numbers and yes/no, with the required fields kept.
func Shape(raw json.RawMessage) map[string]any {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		in = nil
	}
	return shape(in, 0)
}

func shape(in map[string]any, depth int) map[string]any {
	kind, _ := in["type"].(string)
	if list, ok := in["type"].([]any); ok && len(list) > 0 {
		kind, _ = list[0].(string)
	}
	switch kind {
	case "string":
		out := map[string]any{"type": "string", "maxLength": 300}
		if e, ok := in["enum"].([]any); ok && len(e) > 0 {
			out = map[string]any{"type": "string", "enum": e}
		}
		return out
	case "integer", "number", "boolean":
		return map[string]any{"type": kind}
	case "array":
		items, _ := in["items"].(map[string]any)
		if depth >= 3 || items == nil {
			return map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "maxLength": 200}}
		}
		return map[string]any{"type": "array", "maxItems": 8, "items": shape(items, depth+1)}
	}
	// An object, or something with no type given: treat as an object.
	props := map[string]any{}
	if p, ok := in["properties"].(map[string]any); ok && depth < 3 {
		for name, v := range p {
			if sub, ok := v.(map[string]any); ok {
				props[name] = shape(sub, depth+1)
			}
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	required := []string{}
	if r, ok := in["required"].([]any); ok {
		for _, v := range r {
			if name, ok := v.(string); ok && props[name] != nil {
				required = append(required, name)
			}
		}
	}
	out["required"] = required
	return out
}
