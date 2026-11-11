package mirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steermi/cli/internal/redact"
)

// event is one agent-neutral transcript entry.
type event struct {
	kind  string // user | text | tool | todos | turn_end
	id    string // entry identity, when the agent has one
	text  string
	name  string
	input json.RawMessage
	todos []any
}

// convertEvents is the shared line loop for agents whose JSONL entries map
// to events: complete lines only, user messages flush the assistant turn,
// identities are prefix:id or prefix:ns:line.
func convertEvents(data []byte, offset int, prefix, ns string, decode func([]byte) []event) ([]Message, int, error) {
	var (
		out     []Message
		current *assistantTurn
		n       int
	)
	id := func(entry string, line int) string {
		if safeID(entry) {
			return prefix + ":" + entry
		}
		return fmt.Sprintf("%s:%s:%d", prefix, ns, line)
	}
	flush := func() {
		if current != nil {
			if m, ok := current.message(); ok {
				m.ExternalID = id(current.firstUUID, current.firstLine)
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
		for _, e := range decode(sc.Bytes()) {
			switch e.kind {
			case "user":
				text := strings.TrimSpace(e.text)
				if text == "" || strings.HasPrefix(text, "<") {
					continue // environment and instruction wrappers
				}
				flush()
				out = append(out, scrub(Message{Role: "user", Content: truncate(redact.String(text)), ExternalID: id(e.id, n), EndLine: n}))
			case "turn_end":
				flush()
			default:
				if current == nil {
					current = &assistantTurn{firstUUID: e.id, firstLine: n}
				}
				switch e.kind {
				case "text":
					current.addText(e.text)
				case "tool":
					current.addTool(e.name, e.input)
				case "todos":
					current.addTodos(e.todos)
				}
				current.lastLine = n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, offset, fmt.Errorf("read transcript: %w", err)
	}
	flush()
	return out, n, nil
}

// ConvertCodex reads a Codex CLI rollout (~/.codex/sessions/…/rollout-*.jsonl):
// `response_item` entries carry user/assistant messages and function calls.
func ConvertCodex(data []byte, offset int, ns string) ([]Message, int, error) {
	return convertEvents(data, offset, "cx", ns, func(b []byte) []event {
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Role      string `json:"role"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Input     string `json:"input"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &l) != nil || l.Type != "response_item" {
			return nil
		}
		p := l.Payload
		switch p.Type {
		case "message":
			var parts []string
			for _, c := range p.Content {
				parts = append(parts, c.Text)
			}
			text := strings.Join(parts, "\n")
			if p.Role == "user" {
				return []event{{kind: "user", id: p.ID, text: text}}
			}
			if p.Role == "assistant" {
				return []event{{kind: "text", id: p.ID, text: text}}
			}
		case "function_call", "custom_tool_call", "local_shell_call":
			args := p.Arguments
			if args == "" {
				args = p.Input
			}
			input := json.RawMessage(args)
			if !json.Valid(input) {
				input, _ = json.Marshal(map[string]string{"input": args})
			}
			if p.Name == "update_plan" {
				return []event{{kind: "todos", id: p.ID, todos: codexPlan(input)}}
			}
			name := p.Name
			if name == "" {
				name = "shell"
			}
			return []event{{kind: "tool", id: p.ID, name: name, input: codexInput(input)}}
		}
		return nil
	})
}

// codexInput flattens Codex's shell argv ({"command":["bash","-lc","…"]})
// so previews show the command.
func codexInput(input json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return input
	}
	if argv, ok := m["command"].([]any); ok {
		parts := make([]string, 0, len(argv))
		for _, a := range argv {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) == 3 && (parts[1] == "-lc" || parts[1] == "-c") {
			parts = parts[2:]
		}
		m["command"] = strings.Join(parts, " ")
		out, _ := json.Marshal(m)
		return out
	}
	return input
}

func codexPlan(input json.RawMessage) []any {
	var in struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal(input, &in) != nil || len(in.Plan) == 0 {
		return nil
	}
	out := make([]any, 0, len(in.Plan))
	for _, s := range in.Plan {
		out = append(out, map[string]string{"content": s.Step, "status": s.Status})
	}
	return out
}

// ConvertCopilot reads a GitHub Copilot CLI session event log
// (~/.copilot/session-state/…): `user.message` and `assistant.message`
// events, the latter with tool requests.
func ConvertCopilot(data []byte, offset int, ns string) ([]Message, int, error) {
	return convertEvents(data, offset, "gh", ns, func(b []byte) []event {
		var l struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Data struct {
				Content      string `json:"content"`
				ToolRequests []struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"toolRequests"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &l) != nil {
			return nil
		}
		switch l.Type {
		case "user.message":
			return []event{{kind: "user", id: l.ID, text: l.Data.Content}}
		case "assistant.message":
			evs := []event{{kind: "text", id: l.ID, text: l.Data.Content}}
			for _, t := range l.Data.ToolRequests {
				input := t.Arguments
				var s string
				if json.Unmarshal(input, &s) == nil && json.Valid([]byte(s)) {
					input = json.RawMessage(s)
				}
				evs = append(evs, event{kind: "tool", id: l.ID, name: t.Name, input: input})
			}
			return evs
		case "assistant.turn_end":
			return []event{{kind: "turn_end"}}
		}
		return nil
	})
}
