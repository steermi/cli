// Package fixture is a synthetic Steermi gateway for contributors and tests.
// It implements device pairing, token refresh, `whoami` and `next` with fixed
// data, so the CLI can be exercised without a Steermi account. Launches,
// mirroring and merge requests need a real Steermi installation.
//
// Every value here is synthetic (synthetic-test-value).
package fixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// PairingCode is the code `steermi login --no-browser` accepts.
	PairingCode = "stp_fixture"
	// TakenItemID is a work item someone else claims first.
	TakenItemID = "wi-taken"
)

// Call is one request the server received.
type Call struct {
	Method, Path, Auth string
}

// Server is an http.Handler serving the fixture gateway under /v1.
type Server struct {
	mu      sync.Mutex
	calls   []Call
	refresh string
	access  string
	issued  int
	revoked bool
	// Now is a seam for token expiry.
	Now func() time.Time
}

// New returns a fixture gateway with no paired device.
func New() *Server { return &Server{Now: time.Now} }

// Revoke simulates revoking the device in Steermi: refreshes fail with
// DEVICE_REVOKED.
func (s *Server) Revoke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = true
}

// ExpireAccessToken makes the current access token fail with TOKEN_EXPIRED,
// so the client has to refresh.
func (s *Server) ExpireAccessToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access = ""
}

// Calls returns the requests received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{r.Method, r.URL.Path, r.Header.Get("Authorization")})

	switch route := r.Method + " " + r.URL.Path; {
	case route == "POST /v1/ai/devices/exchange":
		var body struct {
			Code     string `json:"code"`
			Verifier string `json:"code_verifier"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Code != PairingCode || body.Verifier == "" {
			fail(w, http.StatusBadRequest, "PAIRING_CODE_INVALID", "the pairing code is invalid or expired")
			return
		}
		s.refresh = "std_fixture_refresh"
		s.revoked = false
		reply(w, s.issue(true))
	case route == "POST /v1/ai/devices/token":
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if s.revoked || s.refresh == "" || body.RefreshToken != s.refresh {
			fail(w, http.StatusUnauthorized, "DEVICE_REVOKED", "this device was revoked")
			return
		}
		reply(w, s.issue(false))
	case !s.authorized(r):
		fail(w, http.StatusUnauthorized, "TOKEN_EXPIRED", "access token expired")
	case route == "GET /v1/ai/devices/me":
		reply(w, map[string]any{"device": device()})
	case route == "GET /v1/work/next":
		reply(w, next())
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/work_items/") && strings.HasSuffix(r.URL.Path, "/claim"):
		if strings.Contains(r.URL.Path, TakenItemID) {
			fail(w, http.StatusConflict, "WORK_ITEM_ALREADY_ASSIGNED", "someone else picked this item up")
			return
		}
		reply(w, map[string]any{})
	default:
		fail(w, http.StatusNotFound, "NOT_IMPLEMENTED_IN_FIXTURE",
			fmt.Sprintf("the fixture gateway does not implement %s; launches need a real Steermi installation", route))
	}
}

func (s *Server) authorized(r *http.Request) bool {
	return s.access != "" && r.Header.Get("Authorization") == "Bearer "+s.access
}

func (s *Server) issue(withRefresh bool) map[string]any {
	s.issued++
	s.access = fmt.Sprintf("fixture-access-%d", s.issued)
	out := map[string]any{
		"access_token": s.access,
		"expires_at":   s.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
		"org_id":       "org-fixture",
		"user_id":      "user-fixture",
		"user_role":    "member",
	}
	if withRefresh {
		out["refresh_token"] = s.refresh
		out["device"] = device()
	}
	return out
}

func device() map[string]any {
	return map[string]any{"id": "dev-fixture", "name": "fixture-laptop", "hostname": "fixture", "os": "linux"}
}

func next() map[string]any {
	days := 4
	return map[string]any{
		"tier":            "continue",
		"in_review_count": 1,
		"teams": []map[string]any{{
			"id": "team-fixture", "name": "Platform",
			"cycle": map[string]any{"name": "Cycle 12", "end_date": "2026-11-03", "days_left": days},
		}},
		"items": []map[string]any{
			{"work_item_id": "wi-1", "identifier": "DEMO-1", "title": "Retry failed webhook deliveries", "type": "story", "status": "in_progress", "priority": "high", "story_points": 3, "assignee_id": "user-fixture", "team_name": "Platform", "project_key": "DEMO", "in_cycle": true, "tier": "continue"},
			{"work_item_id": "wi-2", "identifier": "DEMO-4", "title": "Show sync errors in settings", "type": "task", "status": "todo", "priority": "medium", "story_points": 2, "assignee_id": "user-fixture", "team_name": "Platform", "project_key": "DEMO", "in_cycle": true, "tier": "up_next"},
			{"work_item_id": TakenItemID, "identifier": "DEMO-7", "title": "Fix flaky importer test", "type": "bug", "status": "todo", "priority": "low", "team_name": "Platform", "project_key": "DEMO", "in_cycle": true, "tier": "pick_up"},
		},
	}
}

func reply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
