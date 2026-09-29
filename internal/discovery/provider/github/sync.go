package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// stampMarker hides what devex last wrote beside the idempotency marker, so a sync can tell bundle
// changes from edits made on GitHub.
const stampMarker = "devex-sync"

var stampPattern = regexp.MustCompile(`<!-- ` + stampMarker + `: (.*?) -->`)

// sourceFields are the planned fields a stamp tracks. Parent and dependency links live in the
// body, so a changed relationship surfaces as a body change.
var sourceFields = []string{"title", "body", "labels"}

func (a *Adapter) Sync(
	ctx context.Context,
	target config.Target,
	request provider.SyncRequest,
) ([]provider.Operation, []string, error) {
	prefix := markerPrefix(request.DiscoveryID)
	published, err := a.markedIssues(ctx, target, func(marker string) bool {
		return strings.HasPrefix(marker, prefix)
	})
	if err != nil {
		return nil, nil, err
	}

	warnings := slices.Clone(request.Warnings)
	operations := make([]provider.Operation, 0, len(request.Operations))
	for _, operation := range request.Operations {
		issue, exists := published[operation.IdempotencyKey]
		if !exists {
			operations = append(operations, operation)
			continue
		}
		current := publishedIssue(issue)
		changed := provider.ChangedFields(current.Stamp, operation, sourceFields...)
		// An update keeps every label already on the issue, so a label change adds or does nothing.
		if desired, _ := stringSlice(operation.Fields["labels"]); !slices.ContainsFunc(desired, func(label string) bool {
			return !slices.ContainsFunc(issue.Labels, func(existing *gh.Label) bool { return existing.GetName() == label })
		}) {
			changed = slices.DeleteFunc(changed, func(field string) bool { return field == "labels" })
		}
		synced, warning := provider.SyncIssue(operation, current, changed, request.Force, sourceFields)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		operations = append(operations, synced)
	}
	labels := make(map[string]string, len(published))
	for foundMarker, issue := range published {
		labels[foundMarker] = issueLabel(issue.GetNumber())
	}

	return operations, append(warnings, provider.OrphanWarnings(labels, request.Operations)...), nil
}

func publishedIssue(issue *gh.Issue) provider.PublishedIssue {
	return provider.PublishedIssue{
		Label: issueLabel(issue.GetNumber()),
		Stamp: readStamp(issue.GetBody()),
		Live:  liveDigest(issue.GetTitle(), issue.GetBody()),
	}
}

func issueLabel(number int) string {
	return "GitHub issue #" + strconv.Itoa(number)
}

// executeUpdateIssue rewrites the issue apply resolved for the item, keeping labels people added.
func (a *Adapter) executeUpdateIssue(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
	body string,
	title string,
	labels []string,
) (provider.RemoteRef, error) {
	number, err := strconv.Atoi(resolved[operation.ItemID].Key)
	if err != nil {
		return provider.RemoteRef{}, fmt.Errorf("operation %s has no published issue to update", operation.ID)
	}
	owner, repository := target.GitHub.Owner, target.GitHub.Repository
	current, _, err := a.client.Issues.Get(ctx, owner, repository, number)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	live := liveDigest(current.GetTitle(), current.GetBody())
	written, err := provider.CheckWrite(operation, readStamp(current.GetBody()), live, sourceFields, issueLabel(number))
	if err != nil || written {
		return issueRef(current), err
	}
	for _, label := range current.Labels {
		labels = append(labels, label.GetName())
	}
	labels = provider.UniqueSorted(labels)

	issue, _, err := a.client.Issues.Edit(ctx, owner, repository, number, &gh.IssueRequest{
		Title:  gh.Ptr(title),
		Body:   gh.Ptr(body),
		Labels: &labels,
	})
	if err != nil {
		return provider.RemoteRef{}, err
	}

	return issueRef(issue), nil
}

// stampedBody appends a stamp of what devex is about to write. GitHub stores a body verbatim, so
// the live digest can come from the body being sent rather than from a read-back.
func stampedBody(operation provider.Operation, title string, body string) string {
	encoded, _ := json.Marshal(provider.NewStamp(operation, sourceFields, liveDigest(title, body)))

	return body + "\n<!-- " + stampMarker + ": " + string(encoded) + " -->"
}

// readStamp decodes the stamp in an issue body. Every GitHub stamp records source digests, so one
// that no longer decodes, or decodes without them, was edited by hand. It reads as a write with no
// live digest, which counts as edited.
func readStamp(body string) provider.Stamp {
	var stamp provider.Stamp
	if match := stampPattern.FindStringSubmatch(body); match != nil {
		if err := json.Unmarshal([]byte(match[1]), &stamp); err != nil || stamp.Source == nil {
			return provider.DamagedStamp(stamp.ID)
		}
	}

	return stamp
}

// liveDigest digests an issue's title and body without devex's hidden comments, which change on
// every write but carry no content.
func liveDigest(title string, body string) string {
	body = stampPattern.ReplaceAllString(body, "")
	body = markerPattern.ReplaceAllString(body, "")

	return provider.Digest(title, strings.TrimSpace(body))
}
