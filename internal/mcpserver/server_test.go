package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type call struct {
	method, path string
	body         any
}

type fakeCaller struct {
	calls []call
	reply map[string]string
	err   error
}

func (f *fakeCaller) Do(_ context.Context, method, path string, body, out any) error {
	f.calls = append(f.calls, call{method, path, body})
	if f.err != nil {
		return f.err
	}
	if out != nil {
		raw := f.reply[method+" "+path]
		if raw == "" {
			raw = `{}`
		}
		return json.Unmarshal([]byte(raw), out)
	}
	return nil
}

func tool(t *testing.T, name string) Tool {
	t.Helper()
	for _, tl := range Tools() {
		if tl.Def.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %s not found", name)
	return Tool{}
}

func launch(c Caller) *Launch {
	return &Launch{Runner: "code", Caller: c, WorkItemID: "wi-1", ProjectID: "p-1", SessionID: "s-1", Identifier: "STE-1", Task: func() string { return "# STE-1" }}
}

func TestReadToolsUseOwnScope(t *testing.T) {
	f := &fakeCaller{}
	l := launch(f)
	for name, wantPath := range map[string]string{
		"get_work_item":          "/work_items/wi-1",
		"list_comments":          "/work_items/wi-1/comments",
		"get_work_item_links":    "/work_items/wi-1/links",
		"get_work_item_children": "/work_items/wi-1/children",
	} {
		f.calls = nil
		_, err := tool(t, name).Fn(context.Background(), l, nil)
		require.NoError(t, err, name)
		require.Len(t, f.calls, 1)
		assert.Equal(t, "GET", f.calls[0].method)
		assert.Equal(t, wantPath, f.calls[0].path, name)
	}

	f.calls = nil
	_, err := tool(t, "search").Fn(context.Background(), l, map[string]any{"query": "retry policy"})
	require.NoError(t, err)
	assert.Equal(t, "/search?project_ids=p-1&q=retry+policy", f.calls[0].path)

	f.calls = nil
	_, err = tool(t, "list_work_items").Fn(context.Background(), l, map[string]any{"status": "todo"})
	require.NoError(t, err)
	assert.Equal(t, "/work_items?page_size=50&project_id=p-1&status=todo", f.calls[0].path)

	out, err := tool(t, "steermi_get_task").Fn(context.Background(), l, nil)
	require.NoError(t, err)
	assert.Equal(t, "# STE-1", out)
}

func TestWriteTools(t *testing.T) {
	f := &fakeCaller{reply: map[string]string{"POST /work_items": `{"id":"wi-9","identifier":"STE-9"}`}}
	l := launch(f)

	out, err := tool(t, "create_comment").Fn(context.Background(), l, map[string]any{"body": "Implemented"})
	require.NoError(t, err)
	assert.Contains(t, out, "STE-1")
	assert.Equal(t, map[string]string{"body_markdown": "Implemented"}, f.calls[0].body)

	_, err = tool(t, "update_work_item_status").Fn(context.Background(), l, map[string]any{"status": "in_progress"})
	require.NoError(t, err)
	assert.Equal(t, "PUT", f.calls[1].method)
	assert.Equal(t, map[string]string{"status": "in_progress"}, f.calls[1].body)

	_, err = tool(t, "update_work_item_status").Fn(context.Background(), l, map[string]any{"status": "deleted"})
	assert.Error(t, err)

	f.calls = nil
	out, err = tool(t, "create_follow_up").Fn(context.Background(), l, map[string]any{"title": "Flaky test", "type": "bug", "priority": "high"})
	require.NoError(t, err)
	assert.Equal(t, "Created STE-9.", out)
	require.Len(t, f.calls, 2)
	body := f.calls[0].body.(map[string]any)
	assert.Equal(t, "p-1", body["project_id"], "follow-ups stay in the launch's project")
	assert.Equal(t, "backlog", body["status"])
	assert.Contains(t, body["description_markdown"], "Follow-up from STE-1", "descriptions are sent as markdown for server rendering")
	assert.NotContains(t, body, "description")
	assert.Equal(t, "/work_items/wi-1/links", f.calls[1].path)
	assert.Equal(t, map[string]any{"verb": "relates_to", "target_ids": []string{"wi-9"}}, f.calls[1].body)

	_, err = tool(t, "create_follow_up").Fn(context.Background(), l, map[string]any{"title": "x", "type": "epic"})
	assert.Error(t, err, "epics are not follow-ups")
	_, err = tool(t, "create_comment").Fn(context.Background(), l, map[string]any{"body": "  "})
	assert.Error(t, err)
}

func TestCompleteTaskStoresSummary(t *testing.T) {
	var got string
	l := launch(&fakeCaller{})
	l.Complete = func(s string) error { got = s; return nil }
	out, err := tool(t, "steermi_complete_task").Fn(context.Background(), l, map[string]any{"summary": "Added retries"})
	require.NoError(t, err)
	assert.Equal(t, "Added retries", got)
	assert.Contains(t, out, "/steermi:done")
}

func TestGatewayErrorsSurface(t *testing.T) {
	l := launch(&fakeCaller{err: errors.New("DELEGATED_TOKEN_SCOPE: local launch credentials cannot access this endpoint")})
	_, err := tool(t, "get_work_item").Fn(context.Background(), l, nil)
	assert.ErrorContains(t, err, "DELEGATED_TOKEN_SCOPE")
}

func TestToolNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range Tools() {
		assert.False(t, seen[tl.Def.Name], tl.Def.Name)
		seen[tl.Def.Name] = true
		assert.NotEmpty(t, tl.Def.Description, tl.Def.Name)
	}
	assert.NotNil(t, New(func() (*Launch, error) { return launch(&fakeCaller{}), nil }, "test"))
}

func TestProjectPlanningReadsExplicitSelectedWorkItem(t *testing.T) {
	f := &fakeCaller{}
	l := launch(f)
	l.Runner = "answer"
	l.WorkItemID = ""
	require.True(t, foundationToolAllowed(l, "get_work_item"))
	_, err := tool(t, "get_work_item").Fn(context.Background(), l, nil)
	require.Error(t, err)
	require.Empty(t, f.calls)
	_, err = tool(t, "get_work_item").Fn(context.Background(), l, map[string]any{"item_id": "selected-item"})
	require.NoError(t, err)
	require.Equal(t, "/work_items/selected-item", f.calls[0].path)
}
