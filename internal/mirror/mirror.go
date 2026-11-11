// Package mirror converts new lines of a coding agent's transcript (JSONL:
// Claude Code, Codex CLI, Copilot CLI) into Steermi chat messages, so the team sees a local session in Steermi. The
// message shape matches what the cloud chat worker persists: assistant
// messages carry segments of text, tool calls and todos.
package mirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steermi/cli/internal/redact"
)

// Message is one chat message for POST /v1/ai/chat/sessions/:id/messages.
type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
	Segments  json.RawMessage `json:"segments,omitempty"`
	// ExternalID is the stable identity of the transcript entry the message
	// starts at; Steermi stores each ID once per session, so re-sending a
	// message (a retry, or two processes flushing at once) never duplicates
	// it.
	ExternalID string `json:"external_id"`
	// EndLine is the last transcript line the message consumed: once the
	// message is stored, the mirror offset may advance to it.
	EndLine int `json:"-"`
}

type segment struct {
	Type    string           `json:"type"`
	Content string           `json:"content,omitempty"`
	Tools   []map[string]any `json:"tools,omitempty"`
	Todos   []any            `json:"todos,omitempty"`
}

type line struct {
	UUID        string          `json:"uuid"`
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

type content struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Parser converts transcript data after offset lines into messages and the
// new offset; ns scopes line-number identities (the agent session id).
type Parser func(data []byte, offset int, ns string) ([]Message, int, error)

// maxMessageLen keeps one mirrored message within the gateway body limit.
const maxMessageLen = 200_000

// Convert parses transcript data, skipping the first offset lines, and returns
// the messages plus the new offset. Only complete lines are consumed. ns
// (the Claude session ID) scopes the line-number IDs used for entries
// without a uuid.
func Convert(data []byte, offset int, ns string) ([]Message, int, error) {
	var (
		out     []Message
		current *assistantTurn
		n       int
	)
	flush := func() {
		if current != nil {
			if m, ok := current.message(); ok {
				m.ExternalID = externalID(ns, current.firstUUID, current.firstLine)
				m.EndLine = current.lastLine
				out = append(out, scrub(m))
			}
			current = nil
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	complete := bytes.HasSuffix(data, []byte("\n"))
	total := bytes.Count(data, []byte("\n"))
	for sc.Scan() {
		n++
		if n <= offset {
			continue
		}
		if n > total && !complete {
			n--
			break
		}
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.IsSidechain || len(l.Message) == 0 {
			continue
		}
		switch l.Type {
		case "user":
			if l.IsMeta {
				continue
			}
			text := userText(l.Message)
			if text == "" {
				continue // tool results and command wrappers
			}
			flush()
			out = append(out, scrub(Message{Role: "user", Content: truncate(redact.String(text)), ExternalID: externalID(ns, l.UUID, n), EndLine: n}))
		case "assistant":
			if current == nil {
				current = &assistantTurn{firstUUID: l.UUID, firstLine: n}
			}
			current.add(l.Message)
			current.lastLine = n
		}
	}
	if err := sc.Err(); err != nil {
		return nil, offset, fmt.Errorf("read transcript: %w", err)
	}
	flush()
	return out, n, nil
}

// scrub redacts credentials before a message leaves the machine: the
// transcript includes tool inputs and outputs, where tokens can appear.
func scrub(m Message) Message {
	m.Content = redact.String(m.Content)
	if len(m.ToolCalls) > 0 {
		m.ToolCalls = redact.Bytes(m.ToolCalls)
	}
	if len(m.Segments) > 0 {
		m.Segments = redact.Bytes(m.Segments)
	}
	return m
}

// externalID identifies a message by its first transcript entry: Claude
// Code's per-entry uuid, or the line number within this Claude session.
func externalID(ns, uuid string, line int) string {
	if safeID(uuid) {
		return "cc:" + uuid
	}
	return fmt.Sprintf("cc:%s:%d", ns, line)
}

// safeID keeps IDs within what Steermi accepts (printable ASCII, ≤200).
func safeID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r != '-' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func userText(raw json.RawMessage) string {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return cleanUserText(s)
	}
	var blocks []content
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return cleanUserText(strings.Join(parts, "\n"))
}

// cleanUserText drops Claude Code's internal wrappers (slash command
// expansions, local command output, system reminders).
func cleanUserText(s string) string {
	t := strings.TrimSpace(s)
	for _, prefix := range []string{"<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-caveat>", "<system-reminder>", "Caveat:"} {
		if strings.HasPrefix(t, prefix) {
			return ""
		}
	}
	return t
}

type assistantTurn struct {
	firstUUID           string
	firstLine, lastLine int
	segments            []segment
	tools               []map[string]any
	text                []string
}

func (a *assistantTurn) add(raw json.RawMessage) {
	var msg struct {
		Content []content `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return
	}
	for _, c := range msg.Content {
		switch c.Type {
		case "text":
			a.addText(c.Text)
		case "tool_use":
			if c.Name == "TodoWrite" {
				a.addTodos(todoItems(c.Input))
				continue
			}
			a.addTool(c.Name, c.Input)
		}
	}
}

func (a *assistantTurn) addText(t string) {
	if strings.TrimSpace(t) == "" {
		return
	}
	a.text = append(a.text, t)
	a.segments = append(a.segments, segment{Type: "text", Content: t})
}

func (a *assistantTurn) addTodos(todos []any) {
	if todos != nil {
		a.segments = append(a.segments, segment{Type: "todos", Todos: todos})
	}
}

func (a *assistantTurn) addTool(name string, input json.RawMessage) {
	rec := map[string]any{"name": name, "input": json.RawMessage(orEmptyObject(input)), "preview": preview(name, input)}
	a.tools = append(a.tools, rec)
	if last := len(a.segments) - 1; last >= 0 && a.segments[last].Type == "tools" {
		a.segments[last].Tools = append(a.segments[last].Tools, rec)
	} else {
		a.segments = append(a.segments, segment{Type: "tools", Tools: []map[string]any{rec}})
	}
}

func (a *assistantTurn) message() (Message, bool) {
	if len(a.segments) == 0 {
		return Message{}, false
	}
	text := strings.Join(a.text, "")
	if text == "" {
		text = "*(tool execution only — no text response)*"
	}
	tools := a.tools
	if tools == nil {
		tools = []map[string]any{}
	}
	tc, _ := json.Marshal(tools)
	seg, _ := json.Marshal(a.segments)
	return Message{Role: "assistant", Content: truncate(text), ToolCalls: tc, Segments: seg}, true
}

// todoItems maps TodoWrite input to the chat's todo shape.
func todoItems(input json.RawMessage) []any {
	var in struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if json.Unmarshal(input, &in) != nil || len(in.Todos) == 0 {
		return nil
	}
	out := make([]any, 0, len(in.Todos))
	for _, t := range in.Todos {
		out = append(out, map[string]string{"content": t.Content, "status": t.Status})
	}
	return out
}

// preview is the one-line summary shown on a tool call.
func preview(name string, input json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(input, &m)
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "description", "query"} {
		if v, ok := m[key].(string); ok && v != "" {
			return clip(v, 160)
		}
	}
	if strings.HasPrefix(name, "mcp__") {
		return strings.TrimPrefix(name, "mcp__")
	}
	return clip(string(input), 120)
}

func orEmptyObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func truncate(s string) string {
	if len(s) <= maxMessageLen {
		return s
	}
	return s[:maxMessageLen] + "\n\n…(truncated)"
}
