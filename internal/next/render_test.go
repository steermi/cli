package next

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/steermi/cli/internal/api"
)

func TestRenderAssigned(t *testing.T) {
	three := 3
	n := &api.Next{
		Tier: "up_next",
		Teams: []api.NextTeam{{Name: "Platform", Cycle: &struct {
			Name     string `json:"name"`
			EndDate  string `json:"end_date"`
			DaysLeft *int   `json:"days_left"`
		}{Name: "Cycle 14", DaysLeft: &three}}, {Name: "Mobile"}},
		Items: []api.NextItem{
			{Identifier: "STE-123", Status: "in_progress", Title: "Add retry to webhook sender", Priority: "high", Tier: "continue"},
			{Identifier: "STE-131", Status: "todo", Title: "Rate-limit device endpoints", Priority: "high", Tier: "up_next"},
		},
		InReviewCount: 2,
	}
	out := Render(n)
	assert.Contains(t, out, "Platform · Cycle 14 · 3 days left")
	assert.Contains(t, out, "Mobile · no active cycle")
	assert.Contains(t, out, " Continue\n  1  STE-123   in progress")
	assert.Contains(t, out, " Up next\n  2  STE-131   todo")
	assert.Contains(t, out, "2 waiting for review")
	assert.NotContains(t, out, "picking one assigns it")
}

func TestRenderPickUpAndEmpty(t *testing.T) {
	out := Render(&api.Next{Tier: "pick_up", Items: []api.NextItem{{Identifier: "ATL-25", Status: "todo", Tier: "pick_up"}}})
	assert.Contains(t, out, "Pick up (unassigned, your teams)")
	assert.Contains(t, out, "picking one assigns it to you")
	assert.Contains(t, Render(&api.Next{Tier: "empty"}), "Nothing to pick up right now.")
	assert.Equal(t, "ends today", daysLeft(0))
	assert.Equal(t, "ended", daysLeft(-1))
}
