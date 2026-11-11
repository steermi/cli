// Package next renders `steermi next`.
package next

import (
	"fmt"
	"strings"

	"github.com/steermi/cli/internal/api"
)

// Render formats the list for a terminal.
func Render(n *api.Next) string {
	var b strings.Builder
	for _, t := range n.Teams {
		if t.Cycle != nil {
			fmt.Fprintf(&b, "%s · %s", t.Name, t.Cycle.Name)
			if t.Cycle.DaysLeft != nil {
				fmt.Fprintf(&b, " · %s", daysLeft(*t.Cycle.DaysLeft))
			}
			b.WriteString("\n")
		} else {
			fmt.Fprintf(&b, "%s · no active cycle\n", t.Name)
		}
	}
	if len(n.Teams) == 0 {
		b.WriteString("You are not in any team yet.\n")
	}
	b.WriteString("\n")
	if len(n.Items) == 0 {
		b.WriteString("Nothing to pick up right now.\n")
	}
	section := ""
	for i, it := range n.Items {
		if title := sectionTitle(it.Tier, n.Tier); title != section {
			section = title
			fmt.Fprintf(&b, " %s\n", section)
		}
		fmt.Fprintf(&b, "  %-2d %-9s %-12s %s  %s\n", i+1, it.Identifier, statusLabel(it.Status), clip(it.Title, 48), it.Priority)
	}
	if n.Tier == "pick_up" {
		b.WriteString(" Nothing is assigned to you; picking one assigns it to you.\n")
	}
	if n.InReviewCount > 0 {
		fmt.Fprintf(&b, " %d waiting for review\n", n.InReviewCount)
	}
	b.WriteString("\n")
	return b.String()
}

func sectionTitle(itemTier, listTier string) string {
	switch itemTier {
	case "continue":
		return "Continue"
	case "up_next":
		return "Up next"
	case "pick_up":
		return "Pick up (unassigned, your teams)"
	}
	return listTier
}

func statusLabel(s string) string { return strings.ReplaceAll(s, "_", " ") }

func daysLeft(d int) string {
	switch {
	case d < 0:
		return "ended"
	case d == 0:
		return "ends today"
	case d == 1:
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", d)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
