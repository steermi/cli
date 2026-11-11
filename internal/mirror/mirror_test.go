package mirror

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const transcript = `{"type":"summary","summary":"x"}
{"type":"user","message":{"role":"user","content":"<command-name>/steermi:work</command-name>"}}
{"type":"user","uuid":"u-3","message":{"role":"user","content":"Work on Steermi task STE-1. Read .steermi/TASK.md"}}
{"type":"assistant","uuid":"a-4","message":{"role":"assistant","content":[{"type":"text","text":"Reading the task."}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":".steermi/TASK.md"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"# STE-1"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"TodoWrite","input":{"todos":[{"content":"Add retry","status":"in_progress","activeForm":"Adding retry"}]}}]}}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent noise"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"go test ./..."}},{"type":"tool_use","id":"t4","name":"mcp__steermi__create_comment","input":{"body":"done"}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done: retries added."}]}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}
`

func TestConvert(t *testing.T) {
	msgs, offset, err := Convert([]byte(transcript), 0, "cs-1")
	require.NoError(t, err)
	assert.Equal(t, 11, offset)
	require.Len(t, msgs, 2)

	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "Work on Steermi task STE-1. Read .steermi/TASK.md", msgs[0].Content)

	a := msgs[1]
	assert.Equal(t, "assistant", a.Role)
	assert.Equal(t, "Reading the task.Done: retries added.", a.Content)
	assert.NotContains(t, a.Content, "subagent noise")

	var segs []map[string]any
	require.NoError(t, json.Unmarshal(a.Segments, &segs))
	var types []string
	for _, s := range segs {
		types = append(types, s["type"].(string))
	}
	assert.Equal(t, []string{"text", "tools", "todos", "tools", "text"}, types)

	var tools []map[string]any
	require.NoError(t, json.Unmarshal(a.ToolCalls, &tools))
	require.Len(t, tools, 3, "TodoWrite becomes a todos segment, not a tool call")
	assert.Equal(t, ".steermi/TASK.md", tools[0]["preview"])
	assert.Equal(t, "go test ./...", tools[1]["preview"])
	assert.Equal(t, "steermi__create_comment", tools[2]["preview"])
}

func TestConvertIsIncremental(t *testing.T) {
	_, offset, err := Convert([]byte(transcript), 0, "cs-1")
	require.NoError(t, err)
	more := transcript + `{"type":"user","message":{"role":"user","content":"Now add a test"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Added."}]}}
`
	msgs, newOffset, err := Convert([]byte(more), offset, "cs-1")
	require.NoError(t, err)
	assert.Equal(t, 13, newOffset)
	require.Len(t, msgs, 2)
	assert.Equal(t, "Now add a test", msgs[0].Content)
	assert.Equal(t, "Added.", msgs[1].Content)

	none, same, err := Convert([]byte(more), newOffset, "cs-1")
	require.NoError(t, err)
	assert.Empty(t, none)
	assert.Equal(t, newOffset, same)
}

func TestConvertLeavesPartialLine(t *testing.T) {
	data := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assist`
	msgs, offset, err := Convert([]byte(data), 0, "cs-1")
	require.NoError(t, err)
	assert.Equal(t, 1, offset, "the half-written line is read next time")
	require.Len(t, msgs, 1)
}

func TestConvertToolOnlyTurn(t *testing.T) {
	data := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"a.go"}}]}}
`
	msgs, _, err := Convert([]byte(data), 0, "cs-1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "tool execution only")
}

func TestTruncateLongMessages(t *testing.T) {
	long := strings.Repeat("x", maxMessageLen+10)
	assert.True(t, strings.HasSuffix(truncate(long), "(truncated)"))
}

func TestConvertAssignsStableIdentities(t *testing.T) {
	msgs, offset, err := Convert([]byte(transcript), 0, "cs-1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "cc:u-3", msgs[0].ExternalID, "the transcript entry's uuid")
	assert.Equal(t, 3, msgs[0].EndLine)
	assert.Equal(t, "cc:a-4", msgs[1].ExternalID, "an assistant turn is named by its first entry")
	assert.Equal(t, 10, msgs[1].EndLine)
	assert.Equal(t, 11, offset)

	again, _, err := Convert([]byte(transcript), 0, "cs-1")
	require.NoError(t, err)
	assert.Equal(t, msgs[0].ExternalID, again[0].ExternalID, "re-reading yields the same identities")

	noUUID := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"user","uuid":"bad id!","message":{"role":"user","content":"again"}}
`
	msgs, _, err = Convert([]byte(noUUID), 0, "cs-1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "cc:cs-1:1", msgs[0].ExternalID, "falls back to the line within the Claude session")
	assert.Equal(t, "cc:cs-1:2", msgs[1].ExternalID, "unsafe uuids are not sent")
}

func TestConvertRedactsCredentials(t *testing.T) {
	token := "gh" + "p_" + strings.Repeat("x9", 18)
	data := `{"type":"user","uuid":"u-1","message":{"role":"user","content":"use ` + token + ` to push"}}
{"type":"assistant","uuid":"a-2","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git push https://bot:` + token + `@github.com/a/b.git"}},{"type":"text","text":"Pushed with ` + token + `"}]}}
`
	msgs, _, err := Convert([]byte(data), 0, "cs-1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	for _, m := range msgs {
		for _, field := range []string{m.Content, string(m.ToolCalls), string(m.Segments)} {
			assert.NotContains(t, field, token)
		}
		assert.Contains(t, m.Content, "[REDACTED]")
	}
	var tools []map[string]any
	require.NoError(t, json.Unmarshal(msgs[1].ToolCalls, &tools), "redacted tool calls stay valid JSON")
}

func TestConvertSkipsMalformedLines(t *testing.T) {
	data := "not json\n" +
		`{"type":"user","message":` + "\n" +
		`{"type":"user","message":{"role":"user","content":42}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":"not a list"}}` + "\n" +
		`{"type":"user","uuid":"u-5","message":{"role":"user","content":"real question"}}` + "\n" +
		"\x00\xff garbage\n"
	msgs, offset, err := Convert([]byte(data), 0, "cs-1")
	require.NoError(t, err)
	assert.Equal(t, 6, offset, "malformed lines are consumed, never retried forever")
	require.Len(t, msgs, 1)
	assert.Equal(t, "real question", msgs[0].Content)
}
