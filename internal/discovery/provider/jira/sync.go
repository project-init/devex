package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	gerror "github.com/project-init/gommon/pkg/errors"
	"github.com/project-init/gommon/pkg/jiraclient"
)

// sourceFields are the bundle fields a stamp tracks.
var sourceFields = []string{
	"title",
	"description",
	"acceptance_criteria",
	"document_url",
	"labels",
	"parent_item_id",
}

// writtenTo maps each bundle field to the Jira field an update writes it to.
var writtenTo = map[string]string{
	"title":               "summary",
	"description":         "description",
	"acceptance_criteria": "description",
	"document_url":        "description",
	"labels":              "labels",
	"parent_item_id":      "parent",
}

// liveFields are the Jira fields the live digest covers, and liveSources the bundle fields they
// are written from.
var (
	liveFields  = []string{"summary", "description"}
	liveSources = func() []string {
		var sources []string
		for source, field := range writtenTo {
			if slices.Contains(liveFields, field) {
				sources = append(sources, source)
			}
		}

		return sources
	}()
)

// syncFields are the Jira fields a sync reads: the live digest's inputs, the issue type, the
// labels, and the links.
var syncFields = []string{"summary", "description", "issuetype", "labels", "issuelinks"}

type publishedIssue struct {
	ref       provider.RemoteRef
	stamp     provider.Stamp
	live      string
	issueType string
	labels    []string
	links     []jiraclient.IssueLink
}

// linkEnds identifies a link as the blocked issue sees it.
type linkEnds struct {
	linkType    string
	blockingKey string
	blockedKey  string
}

func (a *Adapter) Sync(
	ctx context.Context,
	target config.Target,
	request provider.SyncRequest,
) ([]provider.Operation, []string, error) {
	published, err := a.publishedIssues(ctx, a.getClient(target.Jira.BaseURL), target, request.DiscoveryID, syncFields...)
	if err != nil {
		return nil, nil, err
	}

	var links []provider.Operation
	var warnings []string
	synced := make(map[domain.ItemID]publishedIssue, len(published))
	operations := make([]provider.Operation, 0, len(request.Operations))
	for _, operation := range request.Operations {
		if operation.Action != provider.ActionCreateIssue {
			links = append(links, operation)
			continue
		}
		issue, exists := published[operation.IdempotencyKey]
		if !exists {
			operations = append(operations, operation)
			continue
		}
		synced[operation.ItemID] = issue
		warnings = append(warnings, unsyncableChanges(operation, issue)...)
		parentID, _ := operation.Fields["parent_item_id"].(string)
		desiredLabels, _ := fieldStringSlice(operation, "labels")
		addsLabels := slices.ContainsFunc(desiredLabels, func(label string) bool { return !slices.Contains(issue.labels, label) })
		changed := jiraFields(provider.ChangedFields(issue.stamp, operation, sourceFields...), parentID != "", addsLabels)
		operation, warning := provider.SyncIssue(operation, provider.PublishedIssue{
			Label: issueLabel(issue.ref.Key),
			Stamp: issue.stamp,
			Live:  issue.live,
		}, changed, request.Force, liveFields)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		operations = append(operations, operation)
	}
	labels := make(map[string]string, len(published))
	for marker, issue := range published {
		labels[marker] = issueLabel(issue.ref.Key)
	}
	warnings = append(warnings, provider.OrphanWarnings(labels, request.Operations)...)

	// The plan's link warning described the links it planned; replace it with one describing the
	// links this sync still changes.
	linkOperations := syncLinks(target, request.DiscoveryID, links, synced)
	planned := linkPermissionWarning(links)
	kept := make([]string, 0, len(request.Warnings)+1)
	for _, warning := range request.Warnings {
		if warning != planned {
			kept = append(kept, warning)
		}
	}
	if warning := linkPermissionWarning(linkOperations); warning != "" {
		kept = append(kept, warning)
	}

	return append(operations, linkOperations...), append(kept, warnings...), nil
}

// publishedIssues finds the issues this discovery published, keyed by idempotency marker, with the
// named fields. An empty discoveryID matches every devex issue in the project.
func (a *Adapter) publishedIssues(
	ctx context.Context,
	client *jiraclient.Client,
	target config.Target,
	discoveryID string,
	fields ...string,
) (map[string]publishedIssue, error) {
	jql := fmt.Sprintf(`project = %q AND labels = %q`, target.Jira.ProjectKey, generatedLabel)
	if discoveryID != "" {
		jql += fmt.Sprintf(` AND labels = %q`, discoveryID)
	}
	search := jiraclient.IssueSearch{JQL: jql, Fields: fields, Properties: []string{propertyKey}, MaxResults: 100}
	published := make(map[string]publishedIssue)
	for {
		result, err := client.SearchIssues(ctx, search)
		if err != nil {
			return nil, err
		}
		for _, issue := range result.Issues {
			stamp, err := readStamp(issue)
			if err != nil {
				return nil, err
			}
			if stamp.ID == "" || (discoveryID != "" && !strings.HasPrefix(stamp.ID, discoveryID+"/")) {
				continue
			}
			state, err := issueState(issue)
			if err != nil {
				return nil, err
			}
			if state.links, err = issue.Links(); err != nil {
				return nil, err
			}
			state.ref = provider.RemoteRef{ID: issue.ID, Key: issue.Key, URL: browseURL(target, issue.Key), Type: "issue"}
			state.stamp = stamp
			// A lost create response and its retry can mark two issues; the original is the oldest.
			if existing, exists := published[stamp.ID]; !exists || olderID(issue.ID, existing.ref.ID) {
				published[stamp.ID] = state
			}
		}
		if result.IsLast || result.NextPageToken == "" {
			return published, nil
		}
		search.NextPageToken = result.NextPageToken
	}
}

// unsyncableChanges warns about bundle changes an update cannot carry to Jira.
func unsyncableChanges(operation provider.Operation, issue publishedIssue) []string {
	var warnings []string
	if desired, _ := operation.Fields["issue_type"].(string); desired != "" && desired != issue.issueType {
		warnings = append(warnings, fmt.Sprintf(
			"Jira %s (%s) is a %s; devex cannot change it to %s.", issue.ref.Key, operation.ItemID, issue.issueType, desired,
		))
	}
	parentID, _ := operation.Fields["parent_item_id"].(string)
	if parentID == "" && len(issue.stamp.Source) > 0 &&
		len(provider.ChangedFields(issue.stamp, operation, "parent_item_id")) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Jira %s (%s) no longer has a parent in the bundle; devex leaves its Jira parent in place.",
			issue.ref.Key, operation.ItemID,
		))
	}

	return warnings
}

// jiraFields maps changed bundle fields to the Jira fields an update rewrites. An item without
// a parent has no parent to write, and an update only adds labels, so it writes labels only when
// some are missing.
func jiraFields(changed []string, hasParent bool, addsLabels bool) []string {
	mapped := make(map[string]bool, len(changed))
	for _, source := range changed {
		switch field := writtenTo[source]; field {
		case "":
		case "labels":
			mapped[field] = addsLabels
		case "parent":
			mapped[field] = hasParent
		default:
			mapped[field] = true
		}
	}
	fields := make([]string, 0, len(mapped))
	for field, write := range mapped {
		if write {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)

	return fields
}

// syncLinks drops planned links that already exist and plans the removal of dependency links
// between this discovery's issues that the bundle no longer declares. Links to issues outside
// the discovery, and links of other types, stay untouched.
func syncLinks(
	target config.Target,
	discoveryID string,
	planned []provider.Operation,
	synced map[domain.ItemID]publishedIssue,
) []provider.Operation {
	itemsByKey := make(map[string]domain.ItemID, len(synced))
	existing := make(map[linkEnds]jiraclient.IssueLink)
	for item, issue := range synced {
		itemsByKey[issue.ref.Key] = item
		for _, link := range issue.links {
			if link.InwardIssue != "" {
				existing[linkEnds{link.Type, link.InwardIssue, issue.ref.Key}] = link
			}
		}
	}

	operations := make([]provider.Operation, 0, len(planned))
	declared := make(map[linkEnds]bool, len(planned))
	for _, operation := range planned {
		ends := plannedLink(operation, synced)
		declared[ends] = true
		if _, exists := existing[ends]; !exists {
			operations = append(operations, operation)
		}
	}

	linkType := linkTypeFor(target)
	var stale []linkEnds
	for ends := range existing {
		if _, inDiscovery := itemsByKey[ends.blockingKey]; ends.linkType == linkType && inDiscovery && !declared[ends] {
			stale = append(stale, ends)
		}
	}
	sort.Slice(stale, func(i, j int) bool {
		if stale[i].blockedKey != stale[j].blockedKey {
			return itemsByKey[stale[i].blockedKey] < itemsByKey[stale[j].blockedKey]
		}
		return itemsByKey[stale[i].blockingKey] < itemsByKey[stale[j].blockingKey]
	})
	for _, ends := range stale {
		blocking, blocked := string(itemsByKey[ends.blockingKey]), string(itemsByKey[ends.blockedKey])
		operations = append(operations, provider.Operation{
			ID:             "unlink-" + blocking + "/" + blocked,
			Action:         provider.ActionUnlinkIssues,
			IdempotencyKey: discoveryID + "/unlink/" + blocking + "/" + blocked,
			Summary:        fmt.Sprintf("Unlink Jira %s: %s blocks %s", linkType, blocking, blocked),
			Fields: map[string]any{
				"link_id":      existing[ends].ID,
				"link_type":    linkType,
				"blocking_key": ends.blockingKey,
				"blocked_key":  ends.blockedKey,
			},
		})
	}

	return operations
}

// plannedLink names a planned link's ends by issue key. An end not yet published has no key,
// so the link cannot match an existing one.
func plannedLink(operation provider.Operation, synced map[domain.ItemID]publishedIssue) linkEnds {
	linkType, _ := operation.Fields["link_type"].(string)
	blockingKey, _ := operation.Fields["blocking_key"].(string)
	if blocking, _ := operation.Fields["blocking_item_id"].(string); blocking != "" {
		blockingKey = synced[domain.ItemID(blocking)].ref.Key
	}
	blocked, _ := operation.Fields["blocked_item_id"].(string)

	return linkEnds{linkType: linkType, blockingKey: blockingKey, blockedKey: synced[domain.ItemID(blocked)].ref.Key}
}

// olderID reports whether Jira issue ID a predates b. Jira assigns numeric IDs in creation order.
func olderID(a string, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}

	return a < b
}

func issueLabel(issueKey string) string {
	return "Jira " + issueKey
}

// issueState reads an issue's live digest, issue type, and labels. The digest covers the summary
// and description as Jira stores them; Jira normalizes the description it receives, so a digest
// must come from a read.
func issueState(issue jiraclient.Issue) (publishedIssue, error) {
	var state publishedIssue
	var summary string
	var description any
	var issueType struct {
		Name string `json:"name"`
	}
	fields := map[string]any{"summary": &summary, "description": &description, "issuetype": &issueType, "labels": &state.labels}
	for name, target := range fields {
		if raw, exists := issue.Fields[name]; exists {
			if err := json.Unmarshal(raw, target); err != nil {
				return publishedIssue{}, fmt.Errorf("decode %s of %s: %w", name, issue.Key, err)
			}
		}
	}
	state.live = provider.Digest(summary, description)
	state.issueType = issueType.Name

	return state, nil
}

// currentState reads an issue's live digest and stamp in one request.
func currentState(ctx context.Context, client *jiraclient.Client, issueKey string) (string, provider.Stamp, error) {
	issue, err := client.GetIssue(ctx, issueKey, []string{"summary", "description"}, []string{propertyKey})
	if err != nil {
		return "", provider.Stamp{}, err
	}
	state, err := issueState(*issue)
	if err != nil {
		return "", provider.Stamp{}, err
	}
	stamp, err := readStamp(*issue)

	return state.live, stamp, err
}

// readStamp decodes an issue's stamp. A stamp that no longer decodes, or that has a live digest
// but no source digests, was changed by hand and reads as damaged. A stamp without a readable
// marker fails: devex can no longer tell which item owns the issue.
func readStamp(issue jiraclient.Issue) (provider.Stamp, error) {
	var stamp provider.Stamp
	raw, exists := issue.Properties[propertyKey]
	if !exists {
		return stamp, nil
	}
	// A field of the wrong type fails the decode but leaves the others, such as the marker, set.
	err := json.Unmarshal(raw, &stamp)
	switch {
	case err != nil && stamp.ID == "":
		return provider.Stamp{}, fmt.Errorf("decode %s of %s: %w", propertyKey, issue.Key, err)
	case err != nil, stamp.Source == nil && stamp.Live != "":
		return provider.DamagedStamp(stamp.ID), nil
	default:
		return stamp, nil
	}
}

// recordLive completes the stamp written with the issue by adding the live digest, which only a
// read-back can give. A failure leaves the write standing: the next sync that changes the issue
// warns that devex cannot tell whether someone edited it, and --force overwrites it.
func recordLive(ctx context.Context, client *jiraclient.Client, operation provider.Operation, issueKey string) {
	live, _, err := currentState(ctx, client, issueKey)
	if err != nil {
		return
	}
	_ = client.SetIssueProperty(ctx, issueKey, propertyKey, provider.NewStamp(operation, sourceFields, live))
}

// executeUpdateIssue rewrites the changed fields of the issue apply resolved for the item. The
// stamp travels in the same request, so an update never lands without a record of it.
func (a *Adapter) executeUpdateIssue(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	remote, exists := resolved[operation.ItemID]
	if !exists {
		return provider.RemoteRef{}, fmt.Errorf("operation %s has no published issue to update", operation.ID)
	}
	client := a.getClient(target.Jira.BaseURL)
	live, stamp, err := currentState(ctx, client, remote.Key)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	written, err := provider.CheckWrite(operation, stamp, live, sourceFields, issueLabel(remote.Key))
	if err != nil {
		return provider.RemoteRef{}, err
	}
	changed, err := fieldStringSlice(operation, "changed")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	// An update that leaves the live fields alone carries the stamp's record of them forward, as
	// read now, so a skipped edit stays pending and devex claims no content it did not write.
	keepsLive := stamp.Source != nil && !slices.ContainsFunc(changed, func(field string) bool {
		return slices.Contains(liveFields, field)
	})
	if written {
		// The write landed; record how Jira stored it if that step never ran.
		if stamp.Live == "" && !keepsLive {
			_ = client.SetIssueProperty(ctx, remote.Key, propertyKey, provider.NewStamp(operation, sourceFields, live))
		}

		return remote, nil
	}

	next := provider.NewStamp(operation, sourceFields, "")
	if keepsLive {
		next.Live = stamp.Live
		for _, name := range liveSources {
			if digest, exists := stamp.Source[name]; exists {
				next.Source[name] = digest
			} else {
				delete(next.Source, name)
			}
		}
	}
	edit := jiraclient.IssueUpdate{
		Fields:     map[string]any{},
		Update:     map[string]any{},
		Properties: map[string]any{propertyKey: next},
	}
	for _, field := range changed {
		switch field {
		case "summary":
			title, err := fieldString(operation, "title")
			if err != nil {
				return provider.RemoteRef{}, err
			}
			edit.Fields["summary"] = title
		case "description":
			description, err := a.descriptionDocument(operation, resolved)
			if err != nil {
				return provider.RemoteRef{}, err
			}
			edit.Fields["description"] = description
		case "parent":
			parent, err := resolveItem(operation, resolved, "parent_item_id")
			if err != nil {
				return provider.RemoteRef{}, err
			}
			edit.Fields["parent"] = map[string]string{"key": parent.Key}
		case "labels":
			labels, err := fieldStringSlice(operation, "labels")
			if err != nil {
				return provider.RemoteRef{}, err
			}
			additions := make([]map[string]string, 0, len(labels))
			for _, label := range labels {
				additions = append(additions, map[string]string{"add": label})
			}
			edit.Update["labels"] = additions
		}
	}
	if err := client.UpdateIssue(ctx, remote.Key, edit); err != nil {
		return provider.RemoteRef{}, err
	}
	if !keepsLive {
		recordLive(ctx, client, operation, remote.Key)
	}

	return remote, nil
}

func (a *Adapter) executeUnlink(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
) (provider.RemoteRef, error) {
	linkID, err := fieldString(operation, "link_id")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	blockedKey, err := fieldString(operation, "blocked_key")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	client := a.getClient(target.Jira.BaseURL)
	if err := client.DeleteIssueLink(ctx, linkID); err != nil {
		if !errors.Is(err, gerror.ErrNotFound) {
			return provider.RemoteRef{}, err
		}
		// Jira also answers 404 to a caller who cannot see the link, so confirm it is gone.
		links, listErr := client.ListIssueLinks(ctx, blockedKey)
		if listErr != nil {
			return provider.RemoteRef{}, listErr
		}
		if slices.ContainsFunc(links, func(link jiraclient.IssueLink) bool { return link.ID == linkID }) {
			return provider.RemoteRef{}, fmt.Errorf("remove link %s from %s: %w", linkID, blockedKey, err)
		}
	}
	delete(a.linkCache, blockedKey)

	return provider.RemoteRef{Key: blockedKey, URL: browseURL(target, blockedKey), Type: "issue_link"}, nil
}
