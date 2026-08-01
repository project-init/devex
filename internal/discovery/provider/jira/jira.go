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
	"sort"
	"strings"
	"time"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

const (
	providerID  = "jira"
	propertyKey = "devex.discovery"
)

type Adapter struct {
	client      *http.Client
	email       string
	token       string
	lookupCache map[string]provider.RemoteRef
	lookupDone  bool
}

func New(authenticated bool) (*Adapter, error) {
	adapter := &Adapter{
		client:      &http.Client{Timeout: 15 * time.Second},
		lookupCache: make(map[string]provider.RemoteRef),
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
	return &Adapter{client: client, email: email, token: token, lookupCache: make(map[string]provider.RemoteRef)}
}

func (a *Adapter) ID() string { return providerID }

func (a *Adapter) Plan(
	_ context.Context,
	workBreakdown *domain.WorkBreakdown,
	_ []byte,
	target config.Target,
) ([]provider.Operation, []string, error) {
	if err := target.Validate(); err != nil {
		return nil, nil, err
	}
	ordered, err := workBreakdown.OrderedItems()
	if err != nil {
		return nil, nil, err
	}
	operations := make([]provider.Operation, 0, len(ordered))
	for _, item := range ordered {
		issueType := jiraIssueType(item.Kind, target.Jira.KindMapping)
		labels := append([]string{"devex-discovery"}, item.Labels...)
		sort.Strings(labels)
		marker := workBreakdown.Discovery.ID + "/" + string(item.ID)
		operations = append(operations, provider.Operation{
			ID:             "create-" + string(item.ID),
			Action:         "create_issue",
			ItemID:         item.ID,
			DependsOn:      dependenciesFor(item),
			IdempotencyKey: marker,
			Summary:        fmt.Sprintf("Create Jira %s for %s", issueType, item.ID),
			Fields: map[string]any{
				"project_key":        target.Jira.ProjectKey,
				"issue_type":         issueType,
				"title":              item.Title,
				"description":        jiraDescription(item, workBreakdown.Discovery.Document),
				"labels":             labels,
				"parent_item_id":     string(item.Parent),
				"idempotency_marker": marker,
			},
		})
	}
	return operations, []string{"Jira dependencies are represented as links in issue descriptions."}, nil
}

func (a *Adapter) Lookup(ctx context.Context, target config.Target, idempotencyKey string) (*provider.RemoteRef, error) {
	if a.lookupDone {
		if remote, exists := a.lookupCache[idempotencyKey]; exists {
			return &remote, nil
		}
		return nil, nil
	}

	nextPageToken := ""
	for {
		query := url.Values{}
		query.Set("jql", fmt.Sprintf(`project = "%s" AND labels = "devex-discovery"`, target.Jira.ProjectKey))
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
			if property.Value.ID != "" {
				a.lookupCache[property.Value.ID] = provider.RemoteRef{
					ID:   issue.ID,
					Key:  issue.Key,
					URL:  strings.TrimSuffix(target.Jira.BaseURL, "/") + "/browse/" + issue.Key,
					Type: "issue",
				}
			}
		}
		if result.IsLast || result.NextPageToken == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}
	a.lookupDone = true
	if remote, exists := a.lookupCache[idempotencyKey]; exists {
		return &remote, nil
	}
	return nil, nil
}

func (a *Adapter) Execute(
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
		"description": adfDocument(description),
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
	return provider.RemoteRef{
		ID:   response.ID,
		Key:  response.Key,
		URL:  strings.TrimSuffix(target.Jira.BaseURL, "/") + "/browse/" + response.Key,
		Type: issueType,
	}, nil
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

func jiraDescription(item domain.WorkItem, document string) string {
	var description strings.Builder
	description.WriteString(item.Description)
	if len(item.AcceptanceCriteria) > 0 {
		description.WriteString("\n\nAcceptance criteria\n")
		for _, criterion := range item.AcceptanceCriteria {
			description.WriteString("- ")
			description.WriteString(criterion)
			description.WriteString("\n")
		}
	}
	if len(item.DependsOn) > 0 {
		description.WriteString("\nDependencies\n")
		for _, dependency := range item.DependsOn {
			description.WriteString("- {{remote:")
			description.WriteString(string(dependency))
			description.WriteString("}}\n")
		}
	}
	description.WriteString("\nDiscovery: ")
	description.WriteString(document)
	return description.String()
}

func adfDocument(text string) map[string]any {
	content := make([]map[string]any, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		content = append(content, map[string]any{
			"type": "paragraph",
			"content": []map[string]any{
				{"type": "text", "text": strings.TrimPrefix(line, "- ")},
			},
		})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
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
