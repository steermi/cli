// Package mcpserver is the steermi stdio MCP server. It gives the local agent
// Steermi tools scoped to its launch: the launch token only reaches the
// launch's work item, project and session (gateway local_launch_auth.go).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/steermi/agent-contracts/activity"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/steermi/cli/internal/config"
)

// Caller performs gateway calls with the launch token.
type Caller interface {
	Do(ctx context.Context, method, path string, body, out any) error
}

// Launch is the subset of the active launch the tools need.
type Launch struct {
	Runner     activity.Runner
	Caller     Caller
	WorkItemID string
	ProjectID  string
	SessionID  string
	Identifier string
	Task       func() string
	// Complete records the agent's summary for `steermi done`.
	Complete   func(summary string) error
	SaveResult func(string) error
}

// Tool is one MCP tool.
type Tool struct {
	Def mcp.Tool
	Fn  func(ctx context.Context, l *Launch, args map[string]any) (string, error)
}

func str(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func get(ctx context.Context, l *Launch, path string) (string, error) {
	var out json.RawMessage
	if err := l.Caller.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return string(out), nil
}

var validStatus = map[string]bool{"todo": true, "in_progress": true, "in_review": true, "done": true, "backlog": true}
var validType = map[string]bool{"story": true, "task": true, "bug": true}
var validPriority = map[string]bool{"low": true, "medium": true, "high": true, "urgent": true, "": true}

// Tools is the local tool set. Names mirror the cloud chat tools where they
// overlap so prompts and skills transfer.
func Tools() []Tool {
	return append(coreTools(), documentTools()...)
}
func coreTools() []Tool {
	return []Tool{
		{
			Def: mcp.NewTool("steermi_get_task",
				mcp.WithDescription("Read this launch's Steermi task brief (TASK.md: ticket, acceptance criteria, comments, instructions)."),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(_ context.Context, l *Launch, _ map[string]any) (string, error) { return l.Task(), nil },
		},
		{
			Def: mcp.NewTool("get_work_item",
				mcp.WithDescription("Get the current work item, or an explicitly selected item_id for a project planning session."),
				mcp.WithString("item_id"),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
				id := l.WorkItemID
				if foundationLaunch(l) {
					id = str(args, "item_id")
				}
				if id == "" {
					return "", fmt.Errorf("item_id is required for project planning")
				}
				return get(ctx, l, "/work_items/"+url.PathEscape(id))
			},
		},
		{
			Def: mcp.NewTool("list_comments",
				mcp.WithDescription("List comments on the current work item."),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(ctx context.Context, l *Launch, _ map[string]any) (string, error) {
				return get(ctx, l, "/work_items/"+url.PathEscape(l.WorkItemID)+"/comments")
			},
		},
		{
			Def: mcp.NewTool("get_work_item_links",
				mcp.WithDescription("List the current work item's links (blocks / is blocked by, relates to, duplicates, causes, clones) with each linked item's status, and whether it is blocked by open work."),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(ctx context.Context, l *Launch, _ map[string]any) (string, error) {
				return get(ctx, l, "/work_items/"+url.PathEscape(l.WorkItemID)+"/links")
			},
		},
		{
			Def: mcp.NewTool("get_work_item_children",
				mcp.WithDescription("List child items (subtasks) of the current work item."),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(ctx context.Context, l *Launch, _ map[string]any) (string, error) {
				return get(ctx, l, "/work_items/"+url.PathEscape(l.WorkItemID)+"/children")
			},
		},
		{
			Def: mcp.NewTool("list_work_items",
				mcp.WithDescription("List work items in this project, optionally by status."),
				mcp.WithString("status", mcp.Description("backlog, todo, in_progress, in_review or done")),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
				q := url.Values{"project_id": {l.ProjectID}, "page_size": {"50"}}
				if s := str(args, "status"); s != "" {
					q.Set("status", s)
				}
				return get(ctx, l, "/work_items?"+q.Encode())
			},
		},
		{
			Def: mcp.NewTool("search",
				mcp.WithDescription("Full-text search across this project's work items and docs."),
				mcp.WithString("query", mcp.Required(), mcp.Description("What to look for")),
				mcp.WithReadOnlyHintAnnotation(true)),
			Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
				q := str(args, "query")
				if q == "" {
					return "", errors.New("query is required")
				}
				return get(ctx, l, "/search?"+url.Values{"q": {q}, "project_ids": {l.ProjectID}}.Encode())
			},
		},
		{
			Def: mcp.NewTool("create_comment",
				mcp.WithDescription("Post a markdown comment on the current work item (progress, decisions, implementation summary)."),
				mcp.WithString("body", mcp.Required(), mcp.Description("Markdown comment"))),
			Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
				body := str(args, "body")
				if body == "" {
					return "", errors.New("body is required")
				}
				var out json.RawMessage
				if err := l.Caller.Do(ctx, http.MethodPost, "/work_items/"+url.PathEscape(l.WorkItemID)+"/comments", map[string]string{"body_markdown": body}, &out); err != nil {
					return "", err
				}
				return "Comment posted on " + l.Identifier + ".", nil
			},
		},
		{
			Def: mcp.NewTool("update_work_item_status",
				mcp.WithDescription("Move the current work item to another status. Usually not needed: opening the merge request moves it to in_review."),
				mcp.WithString("status", mcp.Required(), mcp.Enum("todo", "in_progress", "in_review", "done", "backlog"))),
			Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
				status := str(args, "status")
				if !validStatus[status] {
					return "", fmt.Errorf("invalid status %q", status)
				}
				if err := l.Caller.Do(ctx, http.MethodPut, "/work_items/"+url.PathEscape(l.WorkItemID), map[string]string{"status": status}, nil); err != nil {
					return "", err
				}
				return l.Identifier + " moved to " + status + ".", nil
			},
		},
		{
			Def: mcp.NewTool("create_follow_up",
				mcp.WithDescription("Create a follow-up work item in this project for a real, separate problem found while working, linked to the current item. Do not use it for work that belongs in the current task."),
				mcp.WithString("title", mcp.Required()),
				mcp.WithString("description", mcp.Description("Markdown: problem, where, why it matters")),
				mcp.WithString("type", mcp.Enum("story", "task", "bug")),
				mcp.WithString("priority", mcp.Enum("low", "medium", "high", "urgent"))),
			Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
				title, typ, prio := str(args, "title"), str(args, "type"), str(args, "priority")
				if title == "" {
					return "", errors.New("title is required")
				}
				if typ == "" {
					typ = "task"
				}
				if !validType[typ] || !validPriority[prio] {
					return "", errors.New("invalid type or priority")
				}
				payload := map[string]any{
					"project_id": l.ProjectID, "type": typ, "title": title, "status": "backlog",
					"description_markdown": str(args, "description") + "\n\n---\nFollow-up from " + l.Identifier + " (local Steermi launch).",
				}
				if prio != "" {
					payload["priority"] = prio
				}
				var created struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
				}
				if err := l.Caller.Do(ctx, http.MethodPost, "/work_items", payload, &created); err != nil {
					return "", err
				}
				note := ""
				if created.ID != "" {
					// The gateway records links made with a launch token as origin cli.
					if err := l.Caller.Do(ctx, http.MethodPost, "/work_items/"+url.PathEscape(l.WorkItemID)+"/links",
						map[string]any{"verb": "relates_to", "target_ids": []string{created.ID}}, nil); err != nil {
						note = " (link to " + l.Identifier + " failed)"
					}
				}
				return fmt.Sprintf("Created %s%s.", orDefault(created.Identifier, "follow-up"), note), nil
			},
		},
		{
			Def: mcp.NewTool("steermi_complete_task",
				mcp.WithDescription("Record the final summary of the work (what changed, why, how it was tested, follow-ups). Then ask the developer to run /steermi:done."),
				mcp.WithString("summary", mcp.Required(), mcp.Description("Markdown summary for the merge request and the work item")),
				mcp.WithString("activity_result", mcp.Description(`JSON with outputs (Plan/Build saved docs/work_item references or repository commit), or findings and recommendation (Review), plus optional assumptions (up to 12 short strings: what you assumed instead of knowing). Never include human decisions or verified checks.`))),
			Fn: func(_ context.Context, l *Launch, args map[string]any) (string, error) {
				summary := str(args, "summary")
				if summary == "" {
					return "", errors.New("summary is required")
				}
				if raw := str(args, "activity_result"); raw != "" && l.SaveResult != nil {
					normalized, err := normalizeActivityResult(raw)
					if err != nil {
						return "", err
					}
					if err := l.SaveResult(normalized); err != nil {
						return "", err
					}
				}
				if l.Complete != nil {
					if err := l.Complete(summary); err != nil {
						return "", err
					}
				}
				return "Summary saved. Ask the developer to run /steermi:done to push, open the merge request and post it.", nil
			},
		},
	}
}

// normalizeActivityResult checks an agent's activity_result before it is
// saved: valid JSON, and outputs completed and checked by the contract
// (activity.OutputRef.Normalize: a repository output pins a full commit
// SHA), so the agent fixes its call now instead of the run being refused
// at /steermi:done.
func normalizeActivityResult(raw string) (string, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &result) != nil {
		return "", errors.New("activity_result must be a JSON object")
	}
	rawOutputs, ok := result["outputs"]
	if !ok {
		return raw, nil
	}
	var outputs []activity.OutputRef
	if json.Unmarshal(rawOutputs, &outputs) != nil {
		return "", errors.New("activity_result.outputs must be a list of outputs")
	}
	for i := range outputs {
		if err := outputs[i].Normalize(); err != nil {
			return "", fmt.Errorf("activity_result.outputs[%d].%w", i, err)
		}
	}
	b, err := json.Marshal(outputs)
	if err != nil {
		return "", err
	}
	result["outputs"] = b
	out, err := json.Marshal(result)
	return string(out), err
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Resolver returns the launch for the agent's current directory. It runs on
// every call, so a launch prepared mid-session (/steermi:next) is picked up.
type Resolver func() (*Launch, error)

// New builds the MCP server.
func New(resolve Resolver, version string) *server.MCPServer {
	s := server.NewMCPServer("steermi", version, server.WithToolCapabilities(false))
	launch, err := resolve()
	if err != nil {
		return s
	}
	tools := ToolsFor(launch.Runner)
	if foundationLaunch(launch) {
		tools = append(tools, foundationTools()...)
	}
	var sourceReadMu sync.Mutex
	sourceReads := map[string]int{}
	for _, t := range tools {
		if !foundationToolAllowed(launch, t.Def.Name) {
			continue
		}
		t := t
		s.AddTool(t.Def, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			l, err := resolve()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !foundationToolAllowed(l, t.Def.Name) {
				return mcp.NewToolResultError("Outside this activity's capabilities"), nil
			}
			if foundationLaunch(l) && (t.Def.Name == "read_file" || t.Def.Name == "list_directory") {
				sourceReadMu.Lock()
				exhausted := sourceReads[l.SessionID] >= 30
				if !exhausted {
					sourceReads[l.SessionID]++
				}
				sourceReadMu.Unlock()
				if exhausted {
					return mcp.NewToolResultError("Source read budget reached (30 reads). Save partial findings and continue in another explicitly started turn."), nil
				}
			}
			args, _ := req.Params.Arguments.(map[string]any)
			out, err := t.Fn(ctx, l, args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(out), nil
		})
	}
	return s
}

// SaveSummary stores the agent's summary on the launch state.
func SaveSummary(state *config.LaunchState) func(string) error {
	return func(summary string) error {
		path := summaryPath(state.LaunchID)
		if path == "" {
			return errors.New("invalid launch")
		}
		return writeSummary(path, summary)
	}
}
