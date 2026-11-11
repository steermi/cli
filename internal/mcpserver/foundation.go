package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/steermi/agent-contracts/activity"
)

func foundationLaunch(l *Launch) bool { return l.Runner == activity.Answer && l.WorkItemID == "" }
func foundationToolAllowed(l *Launch, name string) bool {
	if !foundationLaunch(l) {
		return localToolAllowed(l.Runner, name)
	}
	return name == "steermi_get_task" || name == "steermi_complete_task" || name == "get_foundation_artifact" || name == "propose_foundation_artifact" || name == "read_file" || name == "list_directory" || name == "get_doc_page" || name == "get_work_item"
}
func foundationArtifactResult(raw json.RawMessage) (string, error) {
	var state struct {
		Artifacts []struct {
			ID       string `json:"artifact_id"`
			Revision int64  `json:"revision"`
			Payload  string `json:"payload_json"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(raw, &state) != nil || len(state.Artifacts) != 1 {
		return "", fmt.Errorf("saved planning artifact unavailable")
	}
	a := state.Artifacts[0]
	return toJSON(map[string]any{"artifact_id": a.ID, "revision": a.Revision, "artifact_hash": activity.Digest(json.RawMessage(a.Payload)), "payload": json.RawMessage(a.Payload)}), nil
}
func foundationTools() []Tool {
	return []Tool{
		{Def: mcp.NewTool("get_foundation_artifact", mcp.WithDescription("Read the current saved project planning artifact, revision and hash.")), Fn: func(ctx context.Context, l *Launch, _ map[string]any) (string, error) {
			var raw json.RawMessage
			if err := l.Caller.Do(ctx, http.MethodGet, "/ai/chat/sessions/"+url.PathEscape(l.SessionID)+"/workflow", nil, &raw); err != nil {
				return "", err
			}
			return foundationArtifactResult(raw)
		}},
		{Def: mcp.NewTool("propose_foundation_artifact", mcp.WithDescription("Save a structured planning proposal for human review. Never applies Docs, context or tickets. Read current artifact first."), mcp.WithString("artifact_id", mcp.Required()), mcp.WithString("proposal_json", mcp.Required())), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
			var proposal json.RawMessage = []byte(str(args, "proposal_json"))
			if !json.Valid(proposal) {
				return "", fmt.Errorf("proposal_json must be valid JSON")
			}
			var raw json.RawMessage
			err := l.Caller.Do(ctx, http.MethodPost, "/ai/chat/sessions/"+url.PathEscape(l.SessionID)+"/workflow/tools", map[string]string{"artifact_id": str(args, "artifact_id"), "command": "propose_foundation_artifact", "command_key": "local:" + uuid.NewString(), "input_json": string(proposal)}, &raw)
			if err != nil {
				return "", err
			}
			return foundationArtifactResult(raw)
		}},
		foundationFileTool("read_file", false), foundationFileTool("list_directory", true),
	}
}
func foundationFileTool(name string, directory bool) Tool {
	return Tool{Def: mcp.NewTool(name, mcp.WithDescription("Read a selected repository at its pinned revision through Steermi. Credential and source scope are enforced by the gateway."), mcp.WithString("repo_id", mcp.Required()), mcp.WithString("path")), Fn: func(ctx context.Context, l *Launch, args map[string]any) (string, error) {
		var raw json.RawMessage
		if err := l.Caller.Do(ctx, http.MethodGet, "/ai/chat/sessions/"+url.PathEscape(l.SessionID)+"/workflow", nil, &raw); err != nil {
			return "", err
		}
		var state struct {
			Run struct {
				Inputs []activity.OutputRef `json:"inputs"`
			} `json:"run"`
		}
		if json.Unmarshal(raw, &state) != nil {
			return "", fmt.Errorf("planning sources unavailable")
		}

		sha := ""
		for _, ref := range state.Run.Inputs {
			if ref.RepositoryID == str(args, "repo_id") {
				sha = ref.CommitSHA
			}
		}
		if sha == "" {
			return "", fmt.Errorf("repository is empty or not selected at a pinned revision")
		}
		q := url.Values{"path": {str(args, "path")}, "ref": {sha}}
		if !directory && q.Get("path") == "" {
			return "", fmt.Errorf("path is required")
		}
		return get(ctx, l, "/integrations/git/repositories/"+url.PathEscape(str(args, "repo_id"))+"/files?"+q.Encode())
	}}
}

// ArtifactSubmission is the local completion reference, not a domain write.
func ArtifactSubmission(id string, revision int64, hash string) string {
	return toJSON(map[string]any{"outputs": []map[string]string{{"kind": "artifact", "resource_id": id, "revision": strconv.FormatInt(revision, 10), "hash": hash}}})
}
