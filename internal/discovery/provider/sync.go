package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
)

// Operation actions shared by adapters and apply.
const (
	ActionCreateIssue  = "create_issue"
	ActionReuseIssue   = "reuse_issue"
	ActionUpdateIssue  = "update_issue"
	ActionLinkIssues   = "link_issues"
	ActionUnlinkIssues = "unlink_issues"
)

// Syncer reconciles already-published work with a bundle. It reads the remote and returns the
// operations that bring it in line, with the plan's warnings revised to match them.
type Syncer interface {
	Sync(context.Context, config.Target, SyncRequest) ([]Operation, []string, error)
}

// SyncRequest carries the planned operations and warnings to reconcile. Force overwrites issues
// edited since devex last wrote them.
type SyncRequest struct {
	DiscoveryID string
	Operations  []Operation
	Warnings    []string
	Force       bool
}

// Stamp records what devex last wrote to a remote issue: one digest per bundle field it wrote
// from, and one of the title and description as the remote stored them.
type Stamp struct {
	ID     string            `json:"id"`
	Source map[string]string `json:"source,omitempty"`
	Live   string            `json:"live,omitempty"`
}

// PublishedIssue is a remote issue a sync matched to a planned item. Label names it for people,
// such as "Jira DEVEX-2".
type PublishedIssue struct {
	Label string
	Stamp Stamp
	Live  string
}

// Digest hashes values into a stable hex string. Maps hash identically whatever their key order,
// because encoding/json sorts map keys.
func Digest(values ...any) string {
	hash := sha256.New()
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			encoded = []byte(err.Error())
		}
		hash.Write(encoded)
		hash.Write([]byte{0})
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// FieldDigests digests each named field of an operation.
func FieldDigests(operation Operation, names ...string) map[string]string {
	digests := make(map[string]string, len(names))
	for _, name := range names {
		digests[name] = Digest(operation.Fields[name])
	}

	return digests
}

// NewStamp records the named fields of the operation just written and the remote's live digest.
func NewStamp(operation Operation, names []string, live string) Stamp {
	return Stamp{ID: operation.IdempotencyKey, Source: FieldDigests(operation, names...), Live: live}
}

// ChangedFields lists the named fields whose digest differs from a stamp. A stamp without source
// digests predates sync, so every field counts as changed and the first sync rewrites the issue.
func ChangedFields(stamp Stamp, operation Operation, names ...string) []string {
	changed := make([]string, 0, len(names))
	for name, digest := range FieldDigests(operation, names...) {
		if stamp.Source == nil || stamp.Source[name] != digest {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)

	return changed
}

// DamagedStamp stands in for a stamp changed by hand. It records a write with no live digest,
// which counts as edited.
func DamagedStamp(id string) Stamp {
	return Stamp{ID: id, Source: map[string]string{}}
}

// Edited reports whether a remote issue may have changed since devex last wrote it. An issue
// stamped before sync existed carries no source digests and counts as unedited. A stamp without
// a live digest records a write devex could not read back, or a stamp damaged by hand, so it
// counts as edited.
func Edited(stamp Stamp, live string) bool {
	return stamp.Source != nil && stamp.Live != live
}

// SyncIssue turns a planned create for a published item into a reuse or an update. changed
// names the remote fields the update rewrites, and liveFields those whose content the live digest
// covers. On an edited issue, the update drops its live fields with a warning; the rest leave
// edits intact, so they proceed. With force, it rewrites every live field instead, so the new
// live digest records only what devex wrote. With nothing left to write, the item keeps its issue.
func SyncIssue(operation Operation, issue PublishedIssue, changed []string, force bool, liveFields []string) (Operation, string) {
	isLive := func(field string) bool { return slices.Contains(liveFields, field) }
	touchesLive := slices.ContainsFunc(changed, isLive)
	var warning string
	edited := touchesLive && Edited(issue.Stamp, issue.Live)
	if edited && force {
		changed = UniqueSorted(append(slices.Clone(changed), liveFields...))
	}
	if edited && !force {
		reason := "changed since devex last wrote it"
		if issue.Stamp.Live == "" {
			reason = "may have changed: devex never recorded how the remote stored its last write"
		}
		skipped := slices.DeleteFunc(slices.Clone(changed), func(field string) bool { return !isLive(field) })
		warning = fmt.Sprintf(
			"%s (%s) %s; skipped %s. Plan with --force to overwrite it.",
			issue.Label, operation.ItemID, reason, strings.Join(skipped, ", "),
		)
		changed = slices.DeleteFunc(slices.Clone(changed), isLive)
		touchesLive = false
	}
	if len(changed) == 0 {
		return reuse(operation, issue.Label), warning
	}

	fields := make(map[string]any, len(operation.Fields)+2)
	for name, value := range operation.Fields {
		fields[name] = value
	}
	fields["changed"] = changed
	if touchesLive || issue.Stamp.Source == nil {
		fields["expected_live"] = issue.Live
	}
	operation.ID = "update-" + string(operation.ItemID)
	operation.Action = ActionUpdateIssue
	operation.Summary = fmt.Sprintf("Update %s for %s: %s", issue.Label, operation.ItemID, strings.Join(changed, ", "))
	if issue.Stamp.Source == nil {
		operation.Summary += " (first sync; devex has no record of what it wrote)"
	}
	operation.Fields = fields

	return operation, warning
}

// CheckWrite decides whether an update still needs to write. It reports written when the stamp
// already records this operation's write, as on a retry of an update that landed before apply
// recorded it. Otherwise it refuses an issue that changed after the plan read it: --force covers
// only the edits the plan saw.
func CheckWrite(operation Operation, stamp Stamp, live string, names []string, label string) (bool, error) {
	if stamp.Source != nil && maps.Equal(stamp.Source, FieldDigests(operation, names...)) {
		return true, nil
	}
	expected, _ := operation.Fields["expected_live"].(string)
	if expected != "" && expected != live {
		return false, fmt.Errorf("%s changed after the plan was made; plan again", label)
	}

	return false, nil
}

// OrphanWarnings names published issues whose markers the bundle no longer plans. labels maps
// each published marker to its issue label.
func OrphanWarnings(labels map[string]string, planned []Operation) []string {
	wanted := make(map[string]bool, len(planned))
	for _, operation := range planned {
		wanted[operation.IdempotencyKey] = true
	}
	var warnings []string
	for marker, label := range labels {
		if !wanted[marker] {
			warnings = append(warnings, fmt.Sprintf("%s (%s) is no longer in the bundle; devex leaves it untouched.", label, marker))
		}
	}
	sort.Strings(warnings)

	return warnings
}

func reuse(operation Operation, label string) Operation {
	operation.ID = "reuse-" + string(operation.ItemID)
	operation.Action = ActionReuseIssue
	operation.Summary = fmt.Sprintf("Keep %s for %s", label, operation.ItemID)

	return operation
}
