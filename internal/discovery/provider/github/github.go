package github

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

const (
	providerID = "github"

	// generatedMarker hides the idempotency key in the issue body so Resolve can recognise
	// issues this tool created when a local receipt is unavailable.
	generatedMarker = "devex-generated-id"
)

var markerPattern = regexp.MustCompile(`<!-- ` + generatedMarker + `: [^>]+ -->`)

type Adapter struct {
	client *gh.Client
}

func New(authenticated bool, target config.Target) (*Adapter, error) {
	client := gh.NewClient(nil)
	if authenticated {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("GITHUB_TOKEN environment variable not set")
		}
		client = client.WithAuthToken(token)
	}
	if target.GitHub != nil && target.GitHub.APIURL != "" {
		baseURL, err := url.Parse(strings.TrimSuffix(target.GitHub.APIURL, "/") + "/")
		if err != nil {
			return nil, fmt.Errorf("parse github.api_url: %w", err)
		}
		client.BaseURL = baseURL
	}
	return &Adapter{client: client}, nil
}

func NewWithClient(client *gh.Client) *Adapter {
	return &Adapter{client: client}
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
	operations := make([]provider.Operation, 0, len(ordered))
	for _, item := range ordered {
		marker := marker(workBreakdown.Discovery.ID, item.ID)
		body := bodyForItem(item, input.DocumentURL, marker)
		labels := append([]string(nil), item.Labels...)
		if label := target.GitHub.KindLabels[item.Kind]; label != "" {
			labels = append(labels, label)
		}
		labels = provider.UniqueSorted(labels)
		dependsOn := dependenciesFor(item)
		operations = append(operations, provider.Operation{
			ID:             "create-" + string(item.ID),
			Action:         "create_issue",
			ItemID:         item.ID,
			DependsOn:      dependsOn,
			IdempotencyKey: marker,
			Summary:        fmt.Sprintf("Create GitHub issue for %s", item.ID),
			Fields: map[string]any{
				"title":  item.Title,
				"body":   body,
				"labels": labels,
				"kind":   string(item.Kind),
			},
		})
	}
	warnings := []string{
		"GitHub hierarchy and dependencies are represented as links in issue bodies.",
		"Initiatives are created as tracking issues rather than a provider-specific epic type.",
	}
	return operations, warnings, nil
}

func (a *Adapter) Resolve(
	ctx context.Context,
	target config.Target,
	_ *provider.Plan,
	pending []provider.Operation,
) (map[string]provider.RemoteRef, error) {
	if a.client == nil {
		return nil, fmt.Errorf("github client is unavailable")
	}
	wanted := make(map[string]bool, len(pending))
	for _, operation := range pending {
		wanted[operation.IdempotencyKey] = true
	}
	published := make(map[string]provider.RemoteRef, len(pending))

	options := &gh.IssueListByRepoOptions{
		State: "all",
		ListOptions: gh.ListOptions{
			PerPage: 100,
		},
	}
	for {
		issues, response, err := a.client.Issues.ListByRepo(ctx, target.GitHub.Owner, target.GitHub.Repository, options)
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if issue.IsPullRequest() {
				continue
			}
			for _, foundMarker := range markerPattern.FindAllString(issue.GetBody(), -1) {
				if !wanted[foundMarker] {
					continue
				}
				published[foundMarker] = provider.RemoteRef{
					ID:   strconv.FormatInt(issue.GetID(), 10),
					Key:  strconv.Itoa(issue.GetNumber()),
					URL:  issue.GetHTMLURL(),
					Type: "issue",
				}
			}
		}
		if response == nil || response.NextPage == 0 {
			return published, nil
		}
		options.ListOptions.Page = response.NextPage
	}
}

func (a *Adapter) Execute(
	ctx context.Context,
	target config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	title, ok := operation.Fields["title"].(string)
	if !ok || title == "" {
		return provider.RemoteRef{}, fmt.Errorf("operation %s has no title", operation.ID)
	}
	body, ok := operation.Fields["body"].(string)
	if !ok {
		return provider.RemoteRef{}, fmt.Errorf("operation %s has no body", operation.ID)
	}
	resolvedBody, err := provider.ResolveReferences(body, resolved)
	if err != nil {
		return provider.RemoteRef{}, err
	}
	labels, err := stringSlice(operation.Fields["labels"])
	if err != nil {
		return provider.RemoteRef{}, err
	}
	issue, _, err := a.client.Issues.Create(ctx, target.GitHub.Owner, target.GitHub.Repository, &gh.IssueRequest{
		Title:  gh.Ptr(title),
		Body:   gh.Ptr(resolvedBody),
		Labels: &labels,
	})
	if err != nil {
		return provider.RemoteRef{}, err
	}
	return provider.RemoteRef{
		ID:   strconv.FormatInt(issue.GetID(), 10),
		Key:  strconv.Itoa(issue.GetNumber()),
		URL:  issue.GetHTMLURL(),
		Type: "issue",
	}, nil
}

func marker(discoveryID string, itemID domain.ItemID) string {
	return fmt.Sprintf("<!-- %s: %s/%s -->", generatedMarker, discoveryID, itemID)
}

func bodyForItem(item domain.WorkItem, documentURL string, idempotencyMarker string) string {
	var body strings.Builder
	body.WriteString(item.Description)
	body.WriteString("\n\n")
	if len(item.AcceptanceCriteria) > 0 {
		body.WriteString("## Acceptance criteria\n\n")
		for _, criterion := range item.AcceptanceCriteria {
			body.WriteString("- ")
			body.WriteString(criterion)
			body.WriteString("\n")
		}
		body.WriteString("\n")
	}
	if item.Parent != "" {
		body.WriteString("Parent: {{remote:")
		body.WriteString(string(item.Parent))
		body.WriteString("}}\n\n")
	}
	if len(item.DependsOn) > 0 {
		body.WriteString("## Dependencies\n\n")
		for _, dependency := range item.DependsOn {
			body.WriteString("- {{remote:")
			body.WriteString(string(dependency))
			body.WriteString("}}\n")
		}
		body.WriteString("\n")
	}
	// A bare filename tells a reader nothing they can act on, so the footer appears only when
	// the document has a URL to point at.
	if documentURL != "" {
		body.WriteString("Discovery: ")
		body.WriteString(documentURL)
		body.WriteString("\n\n")
	}
	body.WriteString(idempotencyMarker)
	return body.String()
}

func dependenciesFor(item domain.WorkItem) []string {
	ids := make([]string, 0, len(item.DependsOn)+1)
	if item.Parent != "" {
		ids = append(ids, "create-"+string(item.Parent))
	}
	for _, dependency := range item.DependsOn {
		ids = append(ids, "create-"+string(dependency))
	}
	return provider.UniqueSorted(ids)
}

func stringSlice(value any) ([]string, error) {
	switch values := value.(type) {
	case []string:
		return values, nil
	case []any:
		result := make([]string, 0, len(values))
		for _, raw := range values {
			item, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("labels must contain strings")
			}
			result = append(result, item)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("labels must be a list of strings")
	}
}
