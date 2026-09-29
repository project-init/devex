package provider

import (
	"strings"
	"testing"
)

// A stamp written by one run must match the next run's digest, whatever order a map was built in.
func TestDigestIgnoresMapOrder(t *testing.T) {
	first := map[string]any{"type": "doc", "version": 1, "content": []any{"a"}}
	second := map[string]any{"content": []any{"a"}, "version": 1, "type": "doc"}

	if Digest("title", first) != Digest("title", second) {
		t.Fatal("equal maps digested differently")
	}
	if Digest("a", "bc") == Digest("ab", "c") {
		t.Fatal("digest must separate its values")
	}
}

func TestChangedFieldsNamesOnlyDifferences(t *testing.T) {
	operation := Operation{Fields: map[string]any{"title": "New", "labels": []string{"x"}}}
	stamp := Stamp{Source: map[string]string{"title": Digest("Old"), "labels": Digest([]string{"x"})}}

	if got := ChangedFields(stamp, operation, "title", "labels"); strings.Join(got, ",") != "title" {
		t.Fatalf("changed = %v, want [title]", got)
	}
}

// Issues published before sync carry no source digests, so the first sync rewrites them.
func TestChangedFieldsTreatsUnstampedIssuesAsChanged(t *testing.T) {
	operation := Operation{Fields: map[string]any{"title": "Same", "labels": []string{"x"}}}

	if got := ChangedFields(Stamp{ID: "audit/WI-001"}, operation, "title", "labels"); len(got) != 2 {
		t.Fatalf("changed = %v, want every field", got)
	}
}

func TestEditedFollowsTheLiveDigest(t *testing.T) {
	written := map[string]string{"title": Digest("Old")}
	if Edited(Stamp{}, "anything") {
		t.Fatal("an issue stamped before sync must count as unedited")
	}
	if !Edited(Stamp{Source: written}, "anything") {
		t.Fatal("a write devex could not read back must count as edited")
	}
	if !Edited(Stamp{Source: written, Live: "before"}, "after") || Edited(Stamp{Source: written, Live: "same"}, "same") {
		t.Fatal("edits must follow the live digest")
	}
}

func TestSyncIssueReusesUpdatesOrSkips(t *testing.T) {
	operation := Operation{ID: "create-WI-001", ItemID: "WI-001", Fields: map[string]any{"title": "New"}}
	written := NewStamp(Operation{Fields: map[string]any{"title": "Old"}}, []string{"title"}, "live")
	tests := []struct {
		name        string
		issue       PublishedIssue
		force       bool
		wantID      string
		wantWarning bool
	}{
		{name: "unchanged", issue: PublishedIssue{Stamp: NewStamp(operation, []string{"title"}, "live"), Live: "live"}, wantID: "reuse-WI-001"},
		{name: "changed", issue: PublishedIssue{Stamp: written, Live: "live"}, wantID: "update-WI-001"},
		{name: "edited", issue: PublishedIssue{Stamp: written, Live: "edited"}, wantID: "reuse-WI-001", wantWarning: true},
		{name: "edited and forced", issue: PublishedIssue{Stamp: written, Live: "edited"}, force: true, wantID: "update-WI-001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := ChangedFields(test.issue.Stamp, operation, "title")
			synced, warning := SyncIssue(operation, test.issue, changed, test.force, []string{"title"})

			if synced.ID != test.wantID || (warning != "") != test.wantWarning {
				t.Fatalf("operation %s, warning %q", synced.ID, warning)
			}
			if synced.ID == "update-WI-001" && synced.Fields["expected_live"] != test.issue.Live {
				t.Fatalf("fields = %v", synced.Fields)
			}
		})
	}
}

func TestCheckWriteAcceptsItsOwnWriteAndRefusesLaterEdits(t *testing.T) {
	names := []string{"title"}
	planned := Operation{Fields: map[string]any{"title": "New", "expected_live": "planned"}}
	tests := []struct {
		name        string
		stamp       Stamp
		live        string
		wantWritten bool
		wantErr     bool
	}{
		{name: "unchanged since the plan", stamp: NewStamp(Operation{Fields: map[string]any{"title": "Old"}}, names, "planned"), live: "planned"},
		{name: "edited after the plan", live: "edited", wantErr: true},
		{name: "landed before apply recorded it", stamp: NewStamp(planned, names, ""), live: "landed", wantWritten: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			written, err := CheckWrite(planned, test.stamp, test.live, names, "Jira A-1")

			if written != test.wantWritten || (err != nil) != test.wantErr {
				t.Fatalf("written = %t, err = %v", written, err)
			}
			if err != nil && !strings.Contains(err.Error(), "plan again") {
				t.Fatalf("err = %v, want a refusal to plan again", err)
			}
		})
	}
}

// On an edited issue, only the fields that could overwrite the edit wait for --force.
func TestSyncIssueKeepsNonLiveChangesOnAnEditedIssue(t *testing.T) {
	operation := Operation{ID: "create-WI-001", ItemID: "WI-001", Fields: map[string]any{"title": "New", "labels": []string{"x"}}}
	written := NewStamp(Operation{Fields: map[string]any{"title": "Old"}}, []string{"title", "labels"}, "live")

	synced, warning := SyncIssue(operation, PublishedIssue{Label: "Jira A-1", Stamp: written, Live: "edited"}, []string{"labels", "title"}, false, []string{"title"})

	if synced.ID != "update-WI-001" || strings.Join(synced.Fields["changed"].([]string), ",") != "labels" {
		t.Fatalf("operation = %s, changed = %v", synced.ID, synced.Fields["changed"])
	}
	if _, guarded := synced.Fields["expected_live"]; !strings.Contains(warning, "skipped title") || guarded {
		t.Fatalf("warning = %q, fields = %v", warning, synced.Fields)
	}
}
