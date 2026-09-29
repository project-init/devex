package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	gerror "github.com/project-init/gommon/pkg/errors"
	"github.com/project-init/gommon/pkg/jiraclient"
)

const (
	providerID  = "jira"
	propertyKey = "devex.discovery"

	generatedLabel = "devex-generated"

	defaultLinkType = "Blocks"

	trackingLinkType = "Relates"
)

var browsePattern = regexp.MustCompile(`^/browse/([A-Z][A-Z0-9_]*-\d+)$`)

type Adapter struct {
	httpClient *http.Client
	email      string
	token      string
	linkCache  map[string]map[string]bool
}

// getClient instantiates our generic client for a specific target base URL dynamically.
func (a *Adapter) getClient(baseURL string) *jiraclient.Client {
	return jiraclient.NewClient(a.httpClient, baseURL, a.email, a.token)
}

func New(authenticated bool) (*Adapter, error) {
	adapter := &Adapter{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		linkCache:  make(map[string]map[string]bool),
	}
	if authenticated {
		adapter.email = os.Getenv("JIRA_EMAIL")
		adapter.token = os.Getenv("JIRA_API_KEY")
		if adapter.email == "" || adapter.token == "" {
			return nil, fmt.Errorf("JIRA_EMAIL and JIRA_API_KEY environment variables must be set")
		}
	}
	return adapter, nil
}

func NewWithClient(client *http.Client, email string, token string) *Adapter {
	return &Adapter{
		httpClient: client,
		email:      email,
		token:      token,
		linkCache:  make(map[string]map[string]bool),
	}
}

func (a *Adapter) ID() string { return providerID }

func (a *Adapter) Plan(
	_ context.Context,
	input provider.PlanInput,
	target config.Target,
) ([]provider.Operation, []string, error) {
	if err := target.Validate(); err != nil {
		return nil, nil, err
	}
	workBreakdown := input.WorkBreakdown
	ordered, err := workBreakdown.OrderedItems()
	if err != nil {
		return nil, nil, err
	}
	linkType := linkTypeFor(target)
	trackingKey := trackingIssueKey(input.TrackingURL, target.Jira.BaseURL)
	operations := make([]provider.Operation, 0, len(ordered))
	links := make([]provider.Operation, 0, len(ordered))
	for _, item := range ordered {
		issueType := jiraIssueType(item.Kind, target.Jira.KindMapping)
		labels := provider.UniqueSorted(append(
			[]string{generatedLabel, workBreakdown.Discovery.ID},
			item.Labels...,
		))
		for _, label := range labels {
			if strings.ContainsFunc(label, unicode.IsSpace) {
				return nil, nil, fmt.Errorf("item %s has label %q; Jira labels cannot contain whitespace", item.ID, label)
			}
		}
		marker := workBreakdown.Discovery.ID + "/" + string(item.ID)
		for _, dependency := range item.DependsOn {
			links = append(links, linkOperation(workBreakdown.Discovery.ID, item.ID, dependency, linkType))
		}
		if trackingKey != "" && item.Kind == domain.KindInitiative {
			links = append(links, trackingOperation(workBreakdown.Discovery.ID, item.ID, trackingKey))
		}
		operations = append(operations, provider.Operation{
			ID:             "create-" + string(item.ID),
			Action:         provider.ActionCreateIssue,
			ItemID:         item.ID,
			DependsOn:      dependenciesFor(item),
			IdempotencyKey: marker,
			Summary:        fmt.Sprintf("Create Jira %s for %s", issueType, item.ID),
			Fields: map[string]any{
				"project_key":         target.Jira.ProjectKey,
				"issue_type":          issueType,
				"title":               item.Title,
				"description":         item.Description,
				"acceptance_criteria": item.AcceptanceCriteria,
				"document_url":        input.DocumentURL,
				"labels":              labels,
				"parent_item_id":      string(item.Parent),
			},
		})
	}
	var warnings []string
	if warning := linkPermissionWarning(links); warning != "" {
		warnings = append(warnings, warning)
	}
	if input.TrackingURL != "" && trackingKey == "" {
		warnings = append(warnings, fmt.Sprintf(
			"The discovery document links %s, which is not an issue in %s; epics will not relate back to it.",
			input.TrackingURL,
			target.Jira.BaseURL,
		))
	}
	return append(operations, links...), warnings, nil
}

// linkPermissionWarning names the permission and link types the planned link additions and
// removals need, or returns "" when a plan changes no links.
func linkPermissionWarning(operations []provider.Operation) string {
	var types []string
	for _, operation := range operations {
		if operation.Action == provider.ActionLinkIssues || operation.Action == provider.ActionUnlinkIssues {
			linkType, _ := operation.Fields["link_type"].(string)
			types = append(types, linkType)
		}
	}
	if len(types) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"Changing issue links needs the Jira Link Issues permission and these link types: %s.",
		strings.Join(provider.UniqueSorted(types), ", "),
	)
}

func linkTypeFor(target config.Target) string {
	if target.Jira.LinkType != "" {
		return target.Jira.LinkType
	}

	return defaultLinkType
}

func browseURL(target config.Target, issueKey string) string {
	return strings.TrimSuffix(target.Jira.BaseURL, "/") + "/browse/" + issueKey
}

func trackingIssueKey(trackingURL string, baseURL string) string {
	if trackingURL == "" {
		return ""
	}
	tracking, err := url.Parse(trackingURL)
	if err != nil {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(tracking.Host, base.Host) {
		return ""
	}
	match := browsePattern.FindStringSubmatch(strings.TrimSuffix(tracking.Path, "/"))
	if match == nil {
		return ""
	}

	return match[1]
}

func trackingOperation(discoveryID string, item domain.ItemID, trackingKey string) provider.Operation {
	return provider.Operation{
		ID:             "link-" + trackingKey + "/" + string(item),
		Action:         provider.ActionLinkIssues,
		DependsOn:      []string{"create-" + string(item)},
		IdempotencyKey: discoveryID + "/link/" + trackingKey + "/" + string(item),
		Summary:        fmt.Sprintf("Link Jira %s: %s relates to %s", trackingLinkType, item, trackingKey),
		Fields: map[string]any{
			"link_type":       trackingLinkType,
			"blocking_key":    trackingKey,
			"blocked_item_id": string(item),
		},
	}
}

func linkOperation(
	discoveryID string,
	item domain.ItemID,
	dependency domain.ItemID,
	linkType string,
) provider.Operation {
	return provider.Operation{
		ID:             "link-" + string(dependency) + "/" + string(item),
		Action:         provider.ActionLinkIssues,
		DependsOn:      []string{"create-" + string(dependency), "create-" + string(item)},
		IdempotencyKey: discoveryID + "/link/" + string(dependency) + "/" + string(item),
		Summary:        fmt.Sprintf("Link Jira %s: %s blocks %s", linkType, dependency, item),
		Fields: map[string]any{
			"link_type":        linkType,
			"blocking_item_id": string(dependency),
			"blocked_item_id":  string(item),
		},
	}
}

func (a *Adapter) Resolve(
	ctx context.Context,
	target config.Target,
	plan *provider.Plan,
	pending []provider.Operation,
) (map[string]provider.RemoteRef, error) {
	published, err := a.publishedIssues(ctx, a.getClient(target.Jira.BaseURL), target, plan.DiscoveryID, "issuelinks")
	if err != nil {
		return nil, err
	}
	refs := make(map[string]provider.RemoteRef, len(pending))
	for _, operation := range pending {
		if issue, exists := published[operation.IdempotencyKey]; exists {
			refs[operation.IdempotencyKey] = issue.ref
		}
	}
	// The search already read every issue's links, so link operations need not read them again.
	for _, issue := range published {
		a.cacheLinks(issue.ref.Key, issue.links)
	}

	return refs, nil
}

func (a *Adapter) Execute(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	switch operation.Action {
	case provider.ActionLinkIssues:
		return a.executeLink(ctx, target, operation, resolved)
	case provider.ActionUnlinkIssues:
		return a.executeUnlink(ctx, target, operation)
	case provider.ActionUpdateIssue:
		return a.executeUpdateIssue(ctx, target, operation, resolved)
	default:
		return a.executeCreateIssue(ctx, target, operation, resolved)
	}
}

func (a *Adapter) executeCreateIssue(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	projectKey, err := fieldString(operation, "project_key")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	issueType, err := fieldString(operation, "issue_type")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	title, err := fieldString(operation, "title")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	description, err := a.descriptionDocument(operation, resolved)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	labels, err := fieldStringSlice(operation, "labels")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	fields := map[string]any{
		"project":     map[string]string{"key": projectKey},
		"issuetype":   map[string]string{"name": issueType},
		"summary":     title,
		"description": description,
		"labels":      labels,
	}
	if parentID, _ := operation.Fields["parent_item_id"].(string); parentID != "" {
		parent, exists := resolved[domain.ItemID(parentID)]
		if !exists {
			return provider.RemoteRef{}, fmt.Errorf("parent %q has not been published", parentID)
		}
		fields["parent"] = map[string]string{"key": parent.Key}
	}
	body := map[string]any{
		"fields": fields,
		"properties": []map[string]any{
			{"key": propertyKey, "value": provider.NewStamp(operation, sourceFields, "")},
		},
	}

	client := a.getClient(target.Jira.BaseURL)
	response, err := client.CreateIssue(ctx, body)
	if err != nil {
		return provider.RemoteRef{}, err
	}

	a.cacheLinks(response.Key, nil)
	recordLive(ctx, client, operation, response.Key)

	return provider.RemoteRef{
		ID:   response.ID,
		Key:  response.Key,
		URL:  browseURL(target, response.Key),
		Type: issueType,
	}, nil
}

// descriptionDocument renders an operation's description, acceptance criteria, and document link
// as the ADF document Jira stores, with remote references resolved.
func (a *Adapter) descriptionDocument(
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (map[string]any, error) {
	description, err := fieldString(operation, "description")
	if err != nil {
		return nil, err
	}
	description, err = provider.ResolveReferences(description, resolved)
	if err != nil {
		return nil, err
	}
	acceptanceCriteria, err := fieldStringSlice(operation, "acceptance_criteria")
	if err != nil {
		return nil, err
	}
	documentURL, _ := operation.Fields["document_url"].(string)

	return jiraclient.ADFDescription(description, acceptanceCriteria, documentURL), nil
}

func (a *Adapter) executeLink(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	linkType, err := fieldString(operation, "link_type")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	blockingKey, _ := operation.Fields["blocking_key"].(string)
	if blockingKey == "" {
		blocking, err := resolveItem(operation, resolved, "blocking_item_id")
		if err != nil {
			return provider.RemoteRef{}, err
		}
		blockingKey = blocking.Key
	}
	blocked, err := resolveItem(operation, resolved, "blocked_item_id")
	if err != nil {
		return provider.RemoteRef{}, err
	}

	linked, err := a.linkExists(ctx, target, blocked.Key, linkType, blockingKey)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	if !linked {
		client := a.getClient(target.Jira.BaseURL)
		if err := client.CreateIssueLink(ctx, linkType, blockingKey, blocked.Key); err != nil {
			return provider.RemoteRef{}, a.describeLinkFailure(ctx, target, linkType, err)
		}
		a.rememberLink(blocked.Key, linkType, blockingKey)
	}
	return provider.RemoteRef{Key: blocked.Key, URL: blocked.URL, Type: "issue_link"}, nil
}

func (a *Adapter) describeLinkFailure(
	ctx context.Context,
	target config.Target,
	linkType string,
	cause error,
) error {
	if !errors.Is(cause, gerror.ErrBadRequest) {
		return cause
	}

	client := a.getClient(target.Jira.BaseURL)
	names, err := client.GetIssueLinkTypes(ctx)
	if err != nil {
		return cause
	}
	sort.Strings(names)
	return fmt.Errorf("%w; link type %q must be one of: %s", cause, linkType, strings.Join(names, ", "))
}

func (a *Adapter) linkExists(
	ctx context.Context,
	target config.Target,
	issueKey string,
	linkType string,
	blockingKey string,
) (bool, error) {
	if cached, exists := a.linkCache[issueKey]; exists {
		return cached[linkKey(linkType, blockingKey)], nil
	}

	client := a.getClient(target.Jira.BaseURL)
	existingLinks, err := client.GetIssueLinks(ctx, issueKey)
	if err != nil {
		return false, err
	}

	return a.cacheLinks(issueKey, existingLinks)[linkKey(linkType, blockingKey)], nil
}

// cacheLinks records and returns an issue's inward links, the ones linkExists checks.
func (a *Adapter) cacheLinks(issueKey string, links []jiraclient.IssueLink) map[string]bool {
	existing := make(map[string]bool, len(links))
	for _, link := range links {
		if link.InwardIssue != "" {
			existing[linkKey(link.Type, link.InwardIssue)] = true
		}
	}
	if a.linkCache == nil {
		a.linkCache = make(map[string]map[string]bool)
	}
	a.linkCache[issueKey] = existing

	return existing
}

func (a *Adapter) rememberLink(issueKey string, linkType string, blockingKey string) {
	if a.linkCache == nil {
		a.linkCache = make(map[string]map[string]bool)
	}
	if a.linkCache[issueKey] == nil {
		a.linkCache[issueKey] = make(map[string]bool)
	}
	a.linkCache[issueKey][linkKey(linkType, blockingKey)] = true
}

func linkKey(linkType string, blockingKey string) string {
	return linkType + "\x00" + blockingKey
}

func resolveItem(
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
	field string,
) (provider.RemoteRef, error) {
	itemID, err := fieldString(operation, field)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	remote, exists := resolved[domain.ItemID(itemID)]
	if !exists {
		return provider.RemoteRef{}, fmt.Errorf("item %q has not been published", itemID)
	}
	return remote, nil
}

func jiraIssueType(kind domain.ItemKind, mappings map[domain.ItemKind]string) string {
	if mapped := mappings[kind]; mapped != "" {
		return mapped
	}
	switch kind {
	case domain.KindInitiative:
		return "Epic"
	case domain.KindFeature:
		return "Story"
	case domain.KindDefect:
		return "Bug"
	default:
		return "Task"
	}
}

func dependenciesFor(item domain.WorkItem) []string {
	dependencies := make([]string, 0, len(item.DependsOn)+1)
	if item.Parent != "" {
		dependencies = append(dependencies, "create-"+string(item.Parent))
	}
	for _, dependency := range item.DependsOn {
		dependencies = append(dependencies, "create-"+string(dependency))
	}
	sort.Strings(dependencies)
	return dependencies
}

func fieldString(operation provider.Operation, name string) (string, error) {
	value, ok := operation.Fields[name].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("operation %s field %s must be a nonempty string", operation.ID, name)
	}
	return value, nil
}

func fieldStringSlice(operation provider.Operation, name string) ([]string, error) {
	switch values := operation.Fields[name].(type) {
	case nil:
		return nil, nil
	case []string:
		return values, nil
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			stringValue, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("operation %s field %s must contain strings", operation.ID, name)
			}
			result = append(result, stringValue)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("operation %s field %s must be a list", operation.ID, name)
	}
}
