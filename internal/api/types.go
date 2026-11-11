package api

import (
	"context"
	"github.com/steermi/agent-contracts/activity"
	"net/http"
	"net/url"
	"strconv"

	"github.com/steermi/agent-contracts/hint"
)

// Device is a paired CLI.
type Device struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	AgentVersions string `json:"agent_versions"`
	LastSeenAt    string `json:"last_seen_at"`
}

// DeviceToken is returned by pairing exchange and refresh.
type DeviceToken struct {
	AccessToken  string  `json:"access_token"`
	ExpiresAt    string  `json:"expires_at"`
	RefreshToken string  `json:"refresh_token"`
	Device       *Device `json:"device"`
	OrgID        string  `json:"org_id"`
	UserID       string  `json:"user_id"`
	UserRole     string  `json:"user_role"`
}

// Launch is a local launch.
type Launch struct {
	ActivityRunID      string          `json:"activity_run_id"`
	InputDigest        string          `json:"input_digest"`
	ActivityGeneration int64           `json:"activity_generation"`
	ID                 string          `json:"id"`
	PublicID           string          `json:"public_id"`
	ProjectID          string          `json:"project_id"`
	WorkItemID         string          `json:"work_item_id"`
	SessionID          string          `json:"session_id"`
	Runner             activity.Runner `json:"runner"`
	Mode               string          `json:"mode"`
	NoAgent            bool            `json:"no_agent"`
	Branch             string          `json:"branch"`
	BaseBranch         string          `json:"base_branch"`
	State              string          `json:"state"`
	DeviceName         string          `json:"device_name"`
	ExpiresAt          string          `json:"expires_at"`
	Command            string          `json:"command"`
}

// Bundle is the claimed launch's context.
type Bundle struct {
	Launch          Launch `json:"launch"`
	LaunchToken     string `json:"launch_token"`
	TokenExpiresAt  string `json:"token_expires_at"`
	SystemPrompt    string `json:"system_prompt"`
	TaskMarkdown    string `json:"task_markdown"`
	WorkItemJSON    string `json:"work_item_json"`
	ClaudeSessionID string `json:"claude_session_id"`
	// ModelHint is Steermi's suggestion (nil from older services).
	ModelHint *hint.Hint `json:"model_hint,omitempty"`
	// SkillName is the run's skill as people see it.
	SkillName string `json:"skill_name"`
}

// Repository is a connected git repository.
type Repository struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	Provider      string `json:"provider"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
}

// NextItem is one entry of `steermi next`.
type NextItem struct {
	WorkItemID  string `json:"work_item_id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	StoryPoints int    `json:"story_points"`
	AssigneeID  string `json:"assignee_id"`
	TeamName    string `json:"team_name"`
	ProjectKey  string `json:"project_key"`
	InCycle     bool   `json:"in_cycle"`
	Tier        string `json:"tier"`
}

// NextTeam is a team header for `steermi next`.
type NextTeam struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Cycle *struct {
		Name     string `json:"name"`
		EndDate  string `json:"end_date"`
		DaysLeft *int   `json:"days_left"`
	} `json:"cycle"`
}

// Next is the response of GET /v1/work/next.
type Next struct {
	Tier          string     `json:"tier"`
	Items         []NextItem `json:"items"`
	InReviewCount int        `json:"in_review_count"`
	Teams         []NextTeam `json:"teams"`
}

// CreateLaunchRequest creates a launch.
type CreateLaunchRequest struct {
	WorkItemID         string `json:"work_item_id,omitempty"`
	WorkItemIdentifier string `json:"work_item_identifier,omitempty"`
	SkillID            string `json:"skill_id,omitempty"`
	Mode               string `json:"mode,omitempty"`
	NoAgent            bool   `json:"no_agent,omitempty"`
	ExtraInstructions  string `json:"extra_instructions,omitempty"`
	BaseBranch         string `json:"base_branch,omitempty"`
}

// --- Device calls ---

// Exchange completes pairing.
func (c *Client) Exchange(ctx context.Context, code, verifier, agentVersions string) (*DeviceToken, error) {
	var out DeviceToken
	err := c.Do(ctx, http.MethodPost, "/ai/devices/exchange", map[string]string{
		"code": code, "code_verifier": verifier, "agent_versions": agentVersions,
	}, &out, NoAuth)
	return &out, err
}

// Me returns the current device.
func (c *Client) Me(ctx context.Context) (*Device, error) {
	var out struct {
		Device *Device `json:"device"`
	}
	if err := c.Do(ctx, http.MethodGet, "/ai/devices/me", nil, &out, DeviceAuth); err != nil {
		return nil, err
	}
	return out.Device, nil
}

// CreateLaunch creates a launch as the device owner.
func (c *Client) CreateLaunch(ctx context.Context, req CreateLaunchRequest) (*Launch, error) {
	var out struct {
		Launch *Launch `json:"launch"`
	}
	if err := c.Do(ctx, http.MethodPost, "/ai/local-launches", req, &out, DeviceAuth); err != nil {
		return nil, err
	}
	return out.Launch, nil
}

// Claim claims a launch and returns its bundle.
func (c *Client) Claim(ctx context.Context, publicID string) (*Bundle, error) {
	var out Bundle
	err := c.Do(ctx, http.MethodPost, "/ai/local-launches/"+url.PathEscape(publicID)+"/claim", map[string]string{}, &out, DeviceAuth)
	return &out, err
}

// GetLaunch reads a launch.
func (c *Client) GetLaunch(ctx context.Context, id string) (*Launch, error) {
	var out struct {
		Launch *Launch `json:"launch"`
	}
	if err := c.Do(ctx, http.MethodGet, "/ai/local-launches/"+url.PathEscape(id), nil, &out, DeviceAuth); err != nil {
		return nil, err
	}
	return out.Launch, nil
}

// RefreshLaunchToken renews a launch token (and the session lock).
func (c *Client) RefreshLaunchToken(ctx context.Context, launchID string) (token, expiresAt string, err error) {
	var out struct {
		LaunchToken string `json:"launch_token"`
		ExpiresAt   string `json:"expires_at"`
	}
	err = c.Do(ctx, http.MethodPost, "/ai/local-launches/"+url.PathEscape(launchID)+"/token", map[string]string{}, &out, DeviceAuth)
	return out.LaunchToken, out.ExpiresAt, err
}

// Next lists what to work on.
func (c *Client) Next(ctx context.Context, teamID string, all bool, limit int) (*Next, error) {
	q := url.Values{}
	if teamID != "" {
		q.Set("team_id", teamID)
	}
	if all {
		q.Set("all", "true")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/work/next"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out Next
	err := c.Do(ctx, http.MethodGet, path, nil, &out, DeviceAuth)
	return &out, err
}

// ClaimWorkItem assigns the caller to an unassigned work item.
func (c *Client) ClaimWorkItem(ctx context.Context, workItemID string) error {
	return c.Do(ctx, http.MethodPost, "/work_items/"+url.PathEscape(workItemID)+"/claim", map[string]string{}, nil, DeviceAuth)
}

// Repositories lists a project's connected repositories.
func (c *Client) Repositories(ctx context.Context, projectID string, auth Auth) ([]Repository, error) {
	var out struct {
		Repositories []Repository `json:"repositories"`
	}
	err := c.Do(ctx, http.MethodGet, "/integrations/git/repositories?project_id="+url.QueryEscape(projectID)+"&page_size=100", nil, &out, auth)
	return out.Repositories, err
}
