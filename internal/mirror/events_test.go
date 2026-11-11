package mirror

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertCodex(t *testing.T) {
	data := []byte(`{"type":"session_meta","payload":{"id":"s1","cwd":"/w"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Work on STE-1"}]}}
{"type":"response_item","payload":{"type":"reasoning","summary":[]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Running tests."}]}}
{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"go test ./...\"]}","call_id":"c1"}}
{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"Fix\",\"status\":\"in_progress\"}]}"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"thanks"}]}}
`)
	msgs, offset, err := ConvertCodex(data, 0, "s1")
	require.NoError(t, err)
	assert.Equal(t, 8, offset)
	require.Len(t, msgs, 3)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "Work on STE-1", msgs[0].Content)
	assert.Equal(t, "cx:s1:3", msgs[0].ExternalID)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "Running tests.", msgs[1].Content)
	var segs []map[string]any
	require.NoError(t, json.Unmarshal(msgs[1].Segments, &segs))
	require.Len(t, segs, 3)
	tools := segs[1]["tools"].([]any)
	assert.Equal(t, "go test ./...", tools[0].(map[string]any)["preview"])
	assert.Equal(t, "todos", segs[2]["type"])
	assert.Equal(t, 7, msgs[1].EndLine)

	again, off2, err := ConvertCodex(data, offset, "s1")
	require.NoError(t, err)
	assert.Empty(t, again)
	assert.Equal(t, offset, off2)
}

func TestConvertCopilot(t *testing.T) {
	data := []byte(`{"type":"session.start","id":"e0","data":{}}
{"type":"user.message","id":"e1","data":{"content":"Review STE-2"}}
{"type":"assistant.message","id":"e2","data":{"content":"Looking.","toolRequests":[{"name":"bash","arguments":{"command":"make test"}}]}}
{"type":"assistant.turn_end","id":"e3","data":{}}
{"type":"assistant.message","id":"e4","data":{"content":"Done."}}
`)
	msgs, _, err := ConvertCopilot(data, 0, "g1")
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, "gh:e1", msgs[0].ExternalID)
	assert.Equal(t, "Looking.", msgs[1].Content)
	assert.Contains(t, string(msgs[1].ToolCalls), "make test")
	assert.Equal(t, "gh:e4", msgs[2].ExternalID)
}

func TestConvertCodexRedacts(t *testing.T) {
	secret := "sk-ant-" + "api03-" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	data := []byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"key ` + secret + `"}]}}` + "\n")
	msgs, _, err := ConvertCodex(data, 0, "s")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.NotContains(t, msgs[0].Content, secret)
}
