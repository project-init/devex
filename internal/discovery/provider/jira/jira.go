package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
)

const (
	providerID  = "jira"
	propertyKey = "devex.discovery"

	// generatedLabel narrows the idempotency search to issues this tool created. Issue
	// properties hold the identity, so removing the label from an issue hides it from Resolve.
	generatedLabel = "devex-generated"

	actionCreateIssue = "create_issue"
	actionLinkIssues  = "link_issues"

	// defaultLinkType expresses depends_on as a blocking relationship. Jira orients a link
	// so that inwardIssue holds the blocker and outwardIssue holds the blocked issue.
	defaultLinkType = "Blocks"

	// trackingLinkType ties an epic back to the issue that prompted the investigation. The
	// relationship is informational, so it stays "Relates" rather than following link_type.
	trackingLinkType = "Relates"
)

// browsePattern extracts the issue key from a Jira browse URL, the form the discovery skill
// writes into a document title.
var browsePattern = regexp.MustCompile(`^/browse/([A-Z][A-Z0-9_]*-\d+)$`)

type Adapter struct {
	client *http.Client
	email  string
	token  string
	// linkCache holds the links already recorded on an issue, keyed by issue key.
	linkCache map[string]map[string]bool
}

func New(authenticated bool) (*Adapter, error) {
	adapter := &Adapter{
		client:    &http.Client{Timeout: 15 * time.Second},
		linkCache: make(map[string]map[string]bool),
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
		client:    client,
		email:     email,
		token:     token,
		linkCache: make(map[string]map[string]bool),
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
	linkType := target.Jira.LinkType
	if linkType == "" {
		linkType = defaultLinkType
	}
	trackingKey := trackingIssueKey(input.TrackingURL, target.Jira.BaseURL)
	operations := make([]provider.Operation, 0, len(ordered))
	links := make([]provider.Operation, 0, len(ordered))
	for _, item := range ordered {
		issueType := jiraIssueType(item.Kind, target.Jira.KindMapping)
		// The discovery ID scopes Resolve to one bundle, so the search cost tracks the bundle
		// rather than every issue this tool has ever created in the project.
		labels := provider.UniqueSorted(append(
			[]string{generatedLabel, workBreakdown.Discovery.ID},
			item.Labels...,
		))
		// Jira rejects whitespace in labels, and labels are only applied once the issue is
		// being created, so catch it here rather than part-way through publishing.
		for _, label := range labels {
			if strings.ContainsFunc(label, unicode.IsSpace) {
				return nil, nil, fmt.Errorf(
					"item %s has label %q; Jira labels cannot contain whitespace",
					item.ID,
					label,
				)
			}
		}
		marker := workBreakdown.Discovery.ID + "/" + string(item.ID)
		for _, dependency := range item.DependsOn {
			links = append(links, linkOperation(workBreakdown.Discovery.ID, item.ID, dependency, linkType))
		}
		// Epics carry the relationship because they are what a reader lands on from a board;
		// repeating it on every task would bury the tracking issue in noise.
		if trackingKey != "" && item.Kind == domain.KindInitiative {
			links = append(links, trackingOperation(workBreakdown.Discovery.ID, item.ID, trackingKey))
		}
		operations = append(operations, provider.Operation{
			ID:             "create-" + string(item.ID),
			Action:         actionCreateIssue,
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
				"idempotency_marker":  marker,
			},
		})
	}
	var warnings []string
	if len(links) > 0 {
		types := []string{linkType}
		if trackingKey != "" {
			types = append(types, trackingLinkType)
		}
		warnings = append(warnings, fmt.Sprintf(
			"Publishing %d issue link(s) needs the Jira Link Issues permission and these link types: %s.",
			len(links),
			strings.Join(provider.UniqueSorted(types), ", "),
		))
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

// trackingIssueKey reports the issue the discovery document links from its title, empty unless
// that link addresses the instance being published to. A document may reference any tracker, and
// only an issue in this instance can be linked.
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

// trackingOperation relates an epic back to the issue that prompted the investigation. The issue
// exists already, so the operation carries its key rather than an item ID to resolve.
func trackingOperation(discoveryID string, item domain.ItemID, trackingKey string) provider.Operation {
	return provider.Operation{
		ID:             "link-" + trackingKey + "/" + string(item),
		Action:         actionLinkIssues,
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

// linkOperation records that dependency blocks item. Link operations carry no ItemID because
// they publish a relationship rather than a work item. The slash separates the two IDs
// unambiguously: item IDs may contain hyphens but never a slash.
func linkOperation(
	discoveryID string,
	item domain.ItemID,
	dependency domain.ItemID,
	linkType string,
) provider.Operation {
	return provider.Operation{
		ID:             "link-" + string(dependency) + "/" + string(item),
		Action:         actionLinkIssues,
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
	wanted := make(map[string]bool, len(pending))
	for _, operation := range pending {
		wanted[operation.IdempotencyKey] = true
	}
	published := make(map[string]provider.RemoteRef, len(pending))

	// The discovery ID scopes the scan to one bundle, so the cost tracks the bundle rather
	// than every issue this tool has created in the project.
	jql := fmt.Sprintf(`project = %q AND labels = %q`, target.Jira.ProjectKey, generatedLabel)
	if plan.DiscoveryID != "" {
		jql += fmt.Sprintf(` AND labels = %q`, plan.DiscoveryID)
	}
	nextPageToken := ""
	for {
		query := url.Values{}
		query.Set("jql", jql)
		query.Set("fields", "key")
		query.Set("maxResults", "100")
		if nextPageToken != "" {
			query.Set("nextPageToken", nextPageToken)
		}
		request, err := a.newRequest(ctx, target, http.MethodGet, "/rest/api/3/search/jql?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var result struct {
			Issues []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"issues"`
			NextPageToken string `json:"nextPageToken"`
			IsLast        bool   `json:"isLast"`
		}
		if err := a.do(request, &result); err != nil {
			return nil, err
		}
		for _, issue := range result.Issues {
			propertyRequest, err := a.newRequest(
				ctx,
				target,
				http.MethodGet,
				"/rest/api/3/issue/"+url.PathEscape(issue.Key)+"/properties/"+url.PathEscape(propertyKey),
				nil,
			)
			if err != nil {
				return nil, err
			}
			var property struct {
				Value struct {
					ID string `json:"id"`
				} `json:"value"`
			}
			if err := a.do(propertyRequest, &property); err != nil {
				if statusError, ok := err.(*httpStatusError); ok && statusError.StatusCode == http.StatusNotFound {
					continue
				}
				return nil, err
			}
			if wanted[property.Value.ID] {
				published[property.Value.ID] = provider.RemoteRef{
					ID:   issue.ID,
					Key:  issue.Key,
					URL:  strings.TrimSuffix(target.Jira.BaseURL, "/") + "/browse/" + issue.Key,
					Type: "issue",
				}
			}
		}
		// Every pending key is accounted for, so the remaining pages cannot add anything.
		if len(published) == len(wanted) || result.IsLast || result.NextPageToken == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}
	return published, nil
}

func (a *Adapter) Execute(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	if operation.Action == actionLinkIssues {
		return a.executeLink(ctx, target, operation, resolved)
	}
	return a.executeCreateIssue(ctx, target, operation, resolved)
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
	description, err := fieldString(operation, "description")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	description, err = provider.ResolveReferences(description, resolved)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	acceptanceCriteria, err := fieldStringSlice(operation, "acceptance_criteria")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	documentURL, _ := operation.Fields["document_url"].(string)
	labels, err := fieldStringSlice(operation, "labels")
	if err != nil {
		return provider.RemoteRef{}, err
	}
	marker, err := fieldString(operation, "idempotency_marker")
	if err != nil {
		return provider.RemoteRef{}, err
	}

	fields := map[string]any{
		"project":     map[string]string{"key": projectKey},
		"issuetype":   map[string]string{"name": issueType},
		"summary":     title,
		"description": adfDescription(description, acceptanceCriteria, documentURL),
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
			{"key": propertyKey, "value": map[string]string{"id": marker}},
		},
	}
	request, err := a.newRequest(ctx, target, http.MethodPost, "/rest/api/3/issue", body)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	var response struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := a.do(request, &response); err != nil {
		return provider.RemoteRef{}, err
	}
	// A just-created issue has no links, so record that rather than asking Jira for it.
	if a.linkCache == nil {
		a.linkCache = make(map[string]map[string]bool)
	}
	a.linkCache[response.Key] = make(map[string]bool)
	return provider.RemoteRef{
		ID:   response.ID,
		Key:  response.Key,
		URL:  strings.TrimSuffix(target.Jira.BaseURL, "/") + "/browse/" + response.Key,
		Type: issueType,
	}, nil
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
	// A tracking link names an issue that already exists, so it carries a key outright; a
	// dependency link names an item this run published and resolves it.
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
		body := map[string]any{
			"type":         map[string]string{"name": linkType},
			"inwardIssue":  map[string]string{"key": blockingKey},
			"outwardIssue": map[string]string{"key": blocked.Key},
		}
		request, err := a.newRequest(ctx, target, http.MethodPost, "/rest/api/3/issueLink", body)
		if err != nil {
			return provider.RemoteRef{}, err
		}
		if err := a.do(request, nil); err != nil {
			return provider.RemoteRef{}, a.describeLinkFailure(ctx, target, linkType, err)
		}
		a.rememberLink(blocked.Key, linkType, blockingKey)
	}
	return provider.RemoteRef{Key: blocked.Key, URL: blocked.URL, Type: "issue_link"}, nil
}

// describeLinkFailure names the instance's link types when Jira rejects the request, because a
// link type that does not exist fails only after every issue has been created.
func (a *Adapter) describeLinkFailure(
	ctx context.Context,
	target config.Target,
	linkType string,
	cause error,
) error {
	statusError, ok := cause.(*httpStatusError)
	if !ok || statusError.StatusCode != http.StatusBadRequest {
		return cause
	}
	request, err := a.newRequest(ctx, target, http.MethodGet, "/rest/api/3/issueLinkType", nil)
	if err != nil {
		return cause
	}
	var response struct {
		IssueLinkTypes []struct {
			Name string `json:"name"`
		} `json:"issueLinkTypes"`
	}
	if err := a.do(request, &response); err != nil {
		return cause
	}
	names := make([]string, 0, len(response.IssueLinkTypes))
	for _, linkTypeName := range response.IssueLinkTypes {
		names = append(names, linkTypeName.Name)
	}
	sort.Strings(names)
	return fmt.Errorf("%w; link type %q must be one of: %s", cause, linkType, strings.Join(names, ", "))
}

// linkExists reports whether issueKey already records linkType against blockingKey. Jira omits
// the viewed issue from each link, so an existing blocker appears as the inward issue. Results
// are cached because an item with several dependencies asks about the same issue repeatedly.
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
	request, err := a.newRequest(
		ctx,
		target,
		http.MethodGet,
		"/rest/api/3/issue/"+url.PathEscape(issueKey)+"?fields=issuelinks",
		nil,
	)
	if err != nil {
		return false, err
	}
	var issue struct {
		Fields struct {
			IssueLinks []struct {
				Type struct {
					Name string `json:"name"`
				} `json:"type"`
				InwardIssue struct {
					Key string `json:"key"`
				} `json:"inwardIssue"`
			} `json:"issuelinks"`
		} `json:"fields"`
	}
	if err := a.do(request, &issue); err != nil {
		return false, err
	}
	existing := make(map[string]bool, len(issue.Fields.IssueLinks))
	for _, link := range issue.Fields.IssueLinks {
		if link.InwardIssue.Key != "" {
			existing[linkKey(link.Type.Name, link.InwardIssue.Key)] = true
		}
	}
	if a.linkCache == nil {
		a.linkCache = make(map[string]map[string]bool)
	}
	a.linkCache[issueKey] = existing
	return existing[linkKey(linkType, blockingKey)], nil
}

// rememberLink records a link this run created so a later edge onto the same issue does not
// refetch it.
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

func (a *Adapter) newRequest(
	ctx context.Context,
	target config.Target,
	method string,
	path string,
	body any,
) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(target.Jira.BaseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(a.email, a.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func (a *Adapter) do(request *http.Request, target any) error {
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 32*1024))
		return &httpStatusError{StatusCode: response.StatusCode, Body: string(body)}
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode Jira response: %w", err)
	}
	return nil
}

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("Jira returned HTTP %d: %s", e.StatusCode, e.Body)
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
		// An absent list and an empty one mean the same thing: an item with no acceptance
		// criteria writes no key, and YAML reads an empty list back as nil.
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
