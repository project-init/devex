package github

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	gh "github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/gommon/pkg/githubclient"
	"golang.org/x/sync/errgroup"
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
	token := ""
	if authenticated {
		resolved, err := githubclient.ResolveToken()
		if err != nil {
			return nil, githubAuthError(err)
		}
		token = resolved
	}

	baseURL := ""
	if target.GitHub != nil {
		baseURL = target.GitHub.APIURL
	}

	ghClient, err := githubclient.New(token, baseURL)
	if err != nil {
		return nil, fmt.Errorf("create github client: %w", err)
	}

	return &Adapter{client: ghClient}, nil
}

func NewWithClient(client *gh.Client) *Adapter {
	return &Adapter{client: client}
}

func githubAuthError(err error) error {
	if errors.Is(err, githubclient.ErrAuthUnavailable) {
		return fmt.Errorf("GitHub auth required (run `gh auth login` or set GITHUB_TOKEN)")
	}
	return err
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
		body := bodyForItem(item, input, marker)
		labels := append([]string(nil), item.Labels...)
		if label := target.GitHub.KindLabels[item.Kind]; label != "" {
			labels = append(labels, label)
		}
		labels = provider.UniqueSorted(labels)
		dependsOn := dependenciesFor(item)
		operations = append(operations, provider.Operation{
			ID:             "create-" + string(item.ID),
			Action:         provider.ActionCreateIssue,
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

// Resolve finds the pending operations' issues among the discovery's. It lists only the
// discovery's label once that label records it labels every issue. Until then it scans the
// repository, labels each of the discovery's issues it finds, and then marks the label
// complete, so a backfill cut short resumes on the next apply. Resolve runs only during apply,
// so it may write.
func (a *Adapter) Resolve(
	ctx context.Context,
	target config.Target,
	plan *provider.Plan,
	pending []provider.Operation,
) (map[string]provider.RemoteRef, error) {
	issues, complete, err := a.markedIssues(ctx, target, plan.DiscoveryID)
	if err != nil {
		return nil, err
	}
	// The scan already found every issue, so a backfill this token may not make, such as one
	// GitHub forbids without push access, leaves the label incomplete for the next apply.
	if !complete && ctx.Err() == nil {
		_ = a.backfill(ctx, target, plan.DiscoveryID, issues)
	}
	published := make(map[string]provider.RemoteRef, len(pending))
	for _, operation := range pending {
		if issue, found := issues[operation.IdempotencyKey]; found {
			published[operation.IdempotencyKey] = issueRef(issue)
		}
	}

	return published, nil
}

// Confirm reads each candidate issue by number, a few at once, which sees an issue the moment
// GitHub creates it and finds one whose discovery label was removed. An issue counts only while it
// is an issue, not a pull request, in the target repository, and its body still carries the
// candidate's marker. A confirmed issue missing the discovery label gets it back, so the next
// label-scoped lookup finds it; after the first refusal, such as from a token that cannot label,
// Confirm stops trying.
func (a *Adapter) Confirm(ctx context.Context, target config.Target, candidates map[string]provider.RemoteRef) (map[string]provider.RemoteRef, error) {
	owner, repository := target.GitHub.Owner, target.GitHub.Repository
	// GitHub's own URL for the repository, so a renamed repository or an API alias still matches
	// the URL each issue reports.
	repo, _, err := a.client.Repositories.Get(ctx, owner, repository)
	if err != nil {
		return nil, fmt.Errorf("read GitHub repository %s/%s: %w", owner, repository, err)
	}
	repositoryURL := strings.TrimSuffix(repo.GetURL(), "/")
	issues := make(map[string]*gh.Issue, len(candidates))
	var mu sync.Mutex
	group, readCtx := errgroup.WithContext(ctx)
	group.SetLimit(confirmReads)
	for marker, ref := range candidates {
		number, err := strconv.Atoi(ref.Key)
		if err != nil {
			continue
		}
		group.Go(func() error {
			issue, response, err := a.client.Issues.Get(readCtx, owner, repository, number)
			// GitHub answers 404 for an issue this token cannot see as well as for one deleted. Any
			// other failure, a 403 included, says nothing about the issue, so it stops apply rather
			// than risk a second copy.
			if response != nil && (response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("read GitHub issue #%d: %w", number, err)
			}
			inRepository := strings.EqualFold(strings.TrimSuffix(issue.GetRepositoryURL(), "/"), repositoryURL)
			if issue.IsPullRequest() || !inRepository || !strings.Contains(issue.GetBody(), marker) {
				return nil
			}
			mu.Lock()
			issues[marker] = issue
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	confirmed := make(map[string]provider.RemoteRef, len(issues))
	labeling := true
	for _, marker := range slices.Sorted(maps.Keys(issues)) {
		if labeling && a.restoreDiscoveryLabel(ctx, target, issues[marker], marker) != nil {
			labeling = false
		}
		confirmed[marker] = issueRef(issues[marker])
	}
	return confirmed, nil
}

// confirmReads bounds the issue reads Confirm makes at once.
const confirmReads = 4

// restoreDiscoveryLabel adds the discovery label marker names to issue when it lacks it.
func (a *Adapter) restoreDiscoveryLabel(ctx context.Context, target config.Target, issue *gh.Issue, marker string) error {
	discoveryID, ok := discoveryOf(marker)
	if !ok || hasLabel(issue, discoveryLabel(discoveryID)) {
		return nil
	}
	_, _, err := a.client.Issues.AddLabelsToIssue(ctx, target.GitHub.Owner, target.GitHub.Repository, issue.GetNumber(), []string{discoveryLabel(discoveryID)})
	return err
}

// hasLabel reports whether issue carries the label name.
func hasLabel(issue *gh.Issue, name string) bool {
	return slices.ContainsFunc(issue.Labels, func(label *gh.Label) bool { return label.GetName() == name })
}

// backfill labels each of the discovery's issues, then records in the label's description that
// it labels them all.
func (a *Adapter) backfill(ctx context.Context, target config.Target, discoveryID string, issues map[string]*gh.Issue) error {
	owner, repository := target.GitHub.Owner, target.GitHub.Repository
	name, description := discoveryLabel(discoveryID), labelDescription(discoveryID)
	for _, issue := range issues {
		if hasLabel(issue, name) {
			continue // an earlier, interrupted backfill reached it
		}
		if _, _, err := a.client.Issues.AddLabelsToIssue(ctx, owner, repository, issue.GetNumber(), []string{name}); err != nil {
			return fmt.Errorf("label GitHub issue #%d for its discovery: %w", issue.GetNumber(), err)
		}
	}
	label := &gh.Label{Name: &name, Description: &description}
	_, response, err := a.client.Issues.EditLabel(ctx, owner, repository, name, label)
	if response != nil && response.StatusCode == http.StatusNotFound {
		label.Color = gh.Ptr("ededed")
		_, _, err = a.client.Issues.CreateLabel(ctx, owner, repository, label)
	}
	if err != nil {
		return fmt.Errorf("record GitHub label %s: %w", name, err)
	}
	return nil
}

// discoveryLabel names the label devex puts on every issue of a discovery. GitHub caps label
// names at 50 characters, so a long ID ends in a digest instead.
func discoveryLabel(discoveryID string) string {
	return fit("discovery:"+discoveryID, 50, discoveryID)
}

// labelDescription marks a discovery label that labels every one of the discovery's issues.
// GitHub caps descriptions at 100 characters.
func labelDescription(discoveryID string) string {
	return fit("devex labels every issue of discovery "+discoveryID, 100, discoveryID)
}

// fit cuts text to limit characters, ending a cut text in a digest of id so cut texts stay
// distinct.
func fit(text string, limit int, id string) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit-9] + "-" + provider.Digest(id)[:8]
}

// markedIssues returns the discovery's issues by idempotency marker. When the discovery's label
// is complete, it lists only that label and reports complete; otherwise it scans every issue.
func (a *Adapter) markedIssues(ctx context.Context, target config.Target, discoveryID string) (map[string]*gh.Issue, bool, error) {
	if a.client == nil {
		return nil, false, fmt.Errorf("github client is unavailable")
	}
	options := &gh.IssueListByRepoOptions{State: "all", ListOptions: gh.ListOptions{PerPage: 100}}
	name := discoveryLabel(discoveryID)
	// Any failure to read the label falls back to the scan.
	if label, _, err := a.client.Issues.GetLabel(ctx, target.GitHub.Owner, target.GitHub.Repository, name); err == nil && label.GetDescription() == labelDescription(discoveryID) {
		options.Labels = []string{name}
	}
	complete := len(options.Labels) > 0
	prefix := markerPrefix(discoveryID)
	marked := make(map[string]*gh.Issue)
	for {
		issues, response, err := a.client.Issues.ListByRepo(ctx, target.GitHub.Owner, target.GitHub.Repository, options)
		if err != nil {
			return nil, false, err
		}
		for _, issue := range issues {
			if issue.IsPullRequest() {
				continue
			}
			for _, foundMarker := range markerPattern.FindAllString(issue.GetBody(), -1) {
				// A copied body copies the marker too; the original is the oldest issue.
				if existing, exists := marked[foundMarker]; strings.HasPrefix(foundMarker, prefix) && (!exists || issue.GetNumber() < existing.GetNumber()) {
					marked[foundMarker] = issue
				}
			}
		}
		if response == nil || response.NextPage == 0 {
			return marked, complete, nil
		}
		options.ListOptions.Page = response.NextPage
	}
}

func issueRef(issue *gh.Issue) provider.RemoteRef {
	return provider.RemoteRef{
		ID:   strconv.FormatInt(issue.GetID(), 10),
		Key:  strconv.Itoa(issue.GetNumber()),
		URL:  issue.GetHTMLURL(),
		Type: "issue",
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
	// Every write carries the discovery label, whatever devex wrote the plan, so a lookup that
	// trusts the label misses nothing devex created.
	if discoveryID, ok := discoveryOf(operation.IdempotencyKey); ok {
		labels = provider.UniqueSorted(append(labels, discoveryLabel(discoveryID)))
	}
	resolvedBody = stampedBody(operation, title, resolvedBody)
	if operation.Action == provider.ActionUpdateIssue {
		return a.executeUpdateIssue(ctx, target, operation, resolved, resolvedBody, title, labels)
	}
	issue, _, err := a.client.Issues.Create(ctx, target.GitHub.Owner, target.GitHub.Repository, &gh.IssueRequest{
		Title:  gh.Ptr(title),
		Body:   gh.Ptr(resolvedBody),
		Labels: &labels,
	})
	if err != nil {
		return provider.RemoteRef{}, err
	}
	// Preflight cannot judge every token, so restore a discovery label GitHub dropped. The issue
	// exists either way, so a failed label must not fail the create.
	_ = a.restoreDiscoveryLabel(ctx, target, issue, operation.IdempotencyKey)
	return issueRef(issue), nil
}

func marker(discoveryID string, itemID domain.ItemID) string {
	return markerPrefix(discoveryID) + string(itemID) + " -->"
}

// discoveryOf reads the discovery ID back out of a marker.
func discoveryOf(marker string) (string, bool) {
	inner, ok := strings.CutPrefix(marker, "<!-- "+generatedMarker+": ")
	slash := strings.LastIndex(inner, "/")
	if !ok || slash <= 0 {
		return "", false
	}
	return inner[:slash], true
}

// markerPrefix starts every marker of one discovery.
func markerPrefix(discoveryID string) string {
	return fmt.Sprintf("<!-- %s: %s/", generatedMarker, discoveryID)
}

func bodyForItem(item domain.WorkItem, input provider.PlanInput, idempotencyMarker string) string {
	var body strings.Builder
	description := item.Description
	if item.Kind == domain.KindInitiative {
		// GitHub renders Mermaid, so the tracking issue keeps the synopsis's diagram.
		description = provider.WithSynopsis(input.Synopsis.WithDiagram, description)
	}
	body.WriteString(description)
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
	// Initiatives are what a reader lands on first, so they carry the link to the issue that
	// prompted the investigation; repeating it on every task would bury it.
	if item.Kind == domain.KindInitiative && input.TrackingURL != "" {
		body.WriteString("Tracking issue: ")
		body.WriteString(input.TrackingURL)
		body.WriteString("\n\n")
	}
	// A bare filename tells a reader nothing they can act on, so the footer appears only when
	// the document has a URL to point at.
	if input.DocumentURL != "" {
		body.WriteString("Discovery: ")
		body.WriteString(input.DocumentURL)
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

// CheckAccess confirms the credentials can read the target repository and that it takes issues.
func (a *Adapter) CheckAccess(ctx context.Context, target config.Target) (string, error) {
	owner, name := target.GitHub.Owner, target.GitHub.Repository
	repository, _, err := a.client.Repositories.Get(ctx, owner, name)
	if err != nil {
		return "", fmt.Errorf("read GitHub repository %s/%s: %w", owner, name, err)
	}
	if repository.GetArchived() {
		return "", fmt.Errorf("GitHub repository %s/%s is archived, so it takes no new issues", owner, name)
	}
	if !repository.GetHasIssues() {
		return "", fmt.Errorf("GitHub repository %s/%s has issues turned off", owner, name)
	}

	return fmt.Sprintf("repository %s/%s is reachable and takes issues", owner, name), nil
}

// Preflight stops apply before it creates issues the token cannot label. GitHub silently drops
// the labels of an issue created without write access, and a lookup that trusts the discovery
// label would miss the issue and create it again. A GitHub App token gets no permissions back,
// so only a reported lack of write access stops apply.
func (a *Adapter) Preflight(ctx context.Context, target config.Target, creates []provider.Operation) error {
	if len(creates) == 0 {
		return nil
	}
	owner, name := target.GitHub.Owner, target.GitHub.Repository
	repository, _, err := a.client.Repositories.Get(ctx, owner, name)
	if err != nil {
		return fmt.Errorf("read GitHub repository %s/%s: %w", owner, name, err)
	}
	if repository.Permissions != nil && !repository.Permissions["push"] {
		return fmt.Errorf("creating issues in GitHub repository %s/%s needs write access; without it, GitHub drops the labels devex uses to find them", owner, name)
	}
	return nil
}
