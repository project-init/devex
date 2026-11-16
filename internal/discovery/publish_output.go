package discovery

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/publish"
	"github.com/project-init/devex/internal/ui"
)

type actionGroup struct{ action, heading string }

// actionGroups orders a plan's actions for display, with each group's heading.
var actionGroups = []actionGroup{
	{provider.ActionCreateIssue, "Create"},
	{provider.ActionUpdateIssue, "Update"},
	{provider.ActionReuseIssue, "Keep"},
	{provider.ActionLinkIssues, "Link"},
	{provider.ActionUnlinkIssues, "Unlink"},
}

// printPlan lists the plan's operations grouped by action, with the before and after of each
// field an update rewrites.
func printPlan(u *ui.UI, path string, plan *provider.Plan) {
	u.Printf("Publication plan: %s\n", path)
	if len(plan.Operations) == 0 {
		u.Printf("Nothing to publish.\n")
		return
	}
	groups := slices.Clone(actionGroups)
	// An action this list doesn't know still prints, after the known ones, under its own name.
	for _, operation := range plan.Operations {
		if !slices.ContainsFunc(groups, func(g actionGroup) bool { return g.action == operation.Action }) {
			groups = append(groups, actionGroup{operation.Action, operation.Action})
		}
	}
	counts := actionCounts(plan.Operations)
	for _, group := range groups {
		if counts[group.action] == 0 {
			continue
		}
		u.Printf("\n%s (%d)\n", u.Heading(group.heading), counts[group.action])
		for _, operation := range plan.Operations {
			if operation.Action != group.action {
				continue
			}
			u.Printf("  - %s\n", operation.Summary)
			for _, field := range slices.Sorted(maps.Keys(operation.Diff)) {
				change := operation.Diff[field]
				if change.Note != "" {
					u.Printf("      %s: %s\n", field, change.Note)
					continue
				}
				u.Printf("      %s: %s → %s\n", field, shown(change.Before), shown(change.After))
			}
		}
	}
}

func shown(value string) string {
	if value == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", value)
}

func actionCounts(operations []provider.Operation) map[string]int {
	counts := map[string]int{}
	for _, operation := range operations {
		counts[operation.Action]++
	}
	return counts
}

// operationName names an operation by its item, or by its own ID for a relationship.
func operationName(progress publish.Progress) string {
	if progress.Result.ItemID != "" {
		return string(progress.Result.ItemID)
	}
	return progress.Operation.ID
}

// failedThisRun reports whether an operation failed in this run, rather than in an earlier one
// the receipt still records.
func failedThisRun(outcomes []publish.Progress) bool {
	return slices.ContainsFunc(outcomes, func(p publish.Progress) bool { return p.Result.Status == "failed" })
}

func printProgress(u *ui.UI, progress publish.Progress) {
	counter := fmt.Sprintf("[%d/%d]", progress.Index, progress.Total)
	name := operationName(progress)
	switch {
	case progress.Result.Status == "failed":
		u.Warn("%s %s failed: %s", counter, name, progress.Result.Error)
	case progress.Resumed:
		u.Status("%s %s %s, from an earlier run: %s", counter, progress.Result.Status, name, remoteURL(progress.Result))
	default:
		u.Status("%s %s %s: %s", counter, progress.Result.Status, name, remoteURL(progress.Result))
	}
}

func remoteURL(result provider.OperationResult) string {
	if result.Remote == nil {
		return ""
	}
	return result.Remote.URL
}

// applySummary counts an apply's outcomes.
type applySummary struct {
	Total    int `json:"total"`
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	Reused   int `json:"reused"`
	Unlinked int `json:"unlinked"`
	Failed   int `json:"failed"`
	NotRun   int `json:"notRun"`
	Resumed  int `json:"resumed"`
}

// summarize counts each planned operation's outcome as the receipt records it, so work an earlier
// run finished counts even when this run stopped before reaching it.
func summarize(plan *provider.Plan, receipt *provider.Receipt, outcomes []publish.Progress) applySummary {
	summary := applySummary{Total: len(plan.Operations)}
	for _, outcome := range outcomes {
		if outcome.Resumed {
			summary.Resumed++
		}
	}
	for _, operation := range plan.Operations {
		result, recorded := receipt.Operations[operation.ID]
		if !recorded {
			summary.NotRun++
			continue
		}
		switch result.Status {
		case "created":
			summary.Created++
		case "updated":
			summary.Updated++
		case "reused":
			summary.Reused++
		case "unlinked":
			summary.Unlinked++
		case "failed":
			summary.Failed++
		}
	}
	return summary
}

func (s applySummary) String() string {
	parts := []string{
		fmt.Sprintf("%d created", s.Created),
		fmt.Sprintf("%d updated", s.Updated),
		fmt.Sprintf("%d reused", s.Reused),
		fmt.Sprintf("%d failed", s.Failed),
	}
	if s.Unlinked > 0 {
		parts = append(parts, fmt.Sprintf("%d unlinked", s.Unlinked))
	}
	if s.NotRun > 0 {
		parts = append(parts, fmt.Sprintf("%d not run", s.NotRun))
	}
	line := "Applied: " + strings.Join(parts, ", ")
	if s.Resumed > 0 {
		line += fmt.Sprintf(" (%d from an earlier run)", s.Resumed)
	}
	return line
}

type applyOperationJSON struct {
	ID      string `json:"id"`
	Item    string `json:"item,omitempty"`
	Status  string `json:"status"`
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
	Resumed bool   `json:"resumed,omitempty"`
}

// applyJSON is apply's --output json document.
func applyJSON(receiptPath, archived, status string, summary applySummary, outcomes []publish.Progress) map[string]any {
	operations := make([]applyOperationJSON, len(outcomes))
	for i, outcome := range outcomes {
		operations[i] = applyOperationJSON{
			ID: outcome.Operation.ID, Item: string(outcome.Result.ItemID), Status: outcome.Result.Status,
			URL: remoteURL(outcome.Result), Error: outcome.Result.Error, Resumed: outcome.Resumed,
		}
	}
	document := map[string]any{"receipt": receiptPath, "status": status, "summary": summary, "operations": operations}
	if archived != "" {
		document["archived"] = archived // the receipt of the plan this one replaced
	}
	return document
}
