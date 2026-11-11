package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/steermi/agent-contracts/activity"
)

func localToolAllowed(runner activity.Runner, name string) bool {
	if _, ok := activity.Lookup(runner); !ok {
		return false
	}
	if name == "steermi_get_task" || name == "steermi_complete_task" {
		return true
	}
	aliases := map[string]string{"update_work_item_status": "update_work_item", "create_follow_up": "report_detected_issues"}
	if mapped := aliases[name]; mapped != "" {
		name = mapped
	}
	return activity.CapabilitiesFor(runner, "project").Allows(name)
}

// ToolsFor lists the tools a local run of runner may call.
func ToolsFor(runner activity.Runner) []Tool {
	out := []Tool{}
	for _, tool := range Tools() {
		if localToolAllowed(runner, tool.Def.Name) {
			out = append(out, tool)
		}
	}
	return out
}

var docsMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func docHTML(markdown string) (string, error) {
	var out bytes.Buffer
	if err := docsMarkdown.Convert([]byte(markdown), &out); err != nil {
		return "", err
	}
	return out.String(), nil
}
func docsCall(ctx context.Context, l *Launch, method, path string, body any) (string, error) {
	var out json.RawMessage
	if err := l.Caller.Do(ctx, method, path, body, &out); err != nil {
		return "", err
	}
	return string(out), nil
}
func documentTools() []Tool {
	return []Tool{
		{Def: mcp.NewTool("list_doc_areas", mcp.WithDescription("List accessible documentation areas in this activity's project.")), Fn: func(ctx context.Context, l *Launch, _ map[string]any) (string, error) {
			return docsCall(ctx, l, http.MethodGet, "/docs/areas?scope=project&scope_id="+url.QueryEscape(l.ProjectID), nil)
		}},
		{Def: mcp.NewTool("get_doc_page", mcp.WithDescription("Read a selected source document. Access and project scope are checked by Steermi."), mcp.WithString("page_id", mcp.Required())), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
			return docsCall(ctx, l, http.MethodGet, "/docs/pages/"+url.PathEscape(str(args, "page_id")), nil)
		}},
		{Def: mcp.NewTool("list_doc_pages", mcp.WithDescription("List pages in an accessible project documentation area."), mcp.WithString("area_id", mcp.Required())), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
			return docsCall(ctx, l, http.MethodGet, "/docs/areas/"+url.PathEscape(str(args, "area_id"))+"/tree", nil)
		}},
		{Def: mcp.NewTool("create_doc_area", mcp.WithDescription("Create a planning output area in this project."), mcp.WithString("name", mcp.Required())), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
			return docsCall(ctx, l, http.MethodPost, "/docs/areas", map[string]string{"scope": "project", "scope_id": l.ProjectID, "name": str(args, "name")})
		}},
		{Def: mcp.NewTool("create_doc_page", mcp.WithDescription("Save planning output as a document. Supply markdown; record the returned page ID in activity_result.outputs."), mcp.WithString("area_id", mcp.Required()), mcp.WithString("title", mcp.Required()), mcp.WithString("content", mcp.Required())), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
			html, err := docHTML(str(args, "content"))
			if err != nil {
				return "", err
			}
			return docsCall(ctx, l, http.MethodPost, "/docs/areas/"+url.PathEscape(str(args, "area_id"))+"/pages", map[string]string{"title": str(args, "title"), "content_html": html})
		}},
		{Def: mcp.NewTool("update_doc_page", mcp.WithDescription("Update a project planning document from markdown."), mcp.WithString("page_id", mcp.Required()), mcp.WithString("content", mcp.Required())), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
			html, err := docHTML(str(args, "content"))
			if err != nil {
				return "", err
			}
			if str(args, "page_id") == "" {
				return "", errors.New("page_id required")
			}
			return docsCall(ctx, l, http.MethodPut, "/docs/pages/"+url.PathEscape(str(args, "page_id")), map[string]string{"content_html": html})
		}},
	}
}
