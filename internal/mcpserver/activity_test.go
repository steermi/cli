package mcpserver

import (
	"github.com/steermi/agent-contracts/activity"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalActivityToolsUseServerCapabilities(t *testing.T) {
	names := func(runner activity.Runner) []string {
		out := []string{}
		for _, tool := range ToolsFor(runner) {
			out = append(out, tool.Def.Name)
		}
		return out
	}
	require.Contains(t, names("answer"), "create_doc_page")
	require.Contains(t, names("answer"), "update_doc_page")
	require.NotContains(t, names("findings"), "update_work_item_status")
	require.NotContains(t, names("findings"), "create_follow_up")
	require.NotContains(t, names("findings"), "create_doc_page")
	require.Contains(t, names("findings"), "get_doc_page")
	require.Contains(t, names("findings"), "steermi_complete_task")
	require.NotContains(t, names("code"), "update_doc_page")
	require.Empty(t, names("unknown"))
}
