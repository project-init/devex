package jira

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/provider"
	gerror "github.com/project-init/gommon/pkg/errors"
	"github.com/project-init/gommon/pkg/jiraclient"
)

// project is the part of Jira's project resource the online checks read.
type project struct {
	Key        string      `json:"key"`
	IssueTypes []issueType `json:"issueTypes"`
}

type issueType struct {
	Name string `json:"name"`
}

type currentUser struct {
	DisplayName string `json:"displayName"`
}

// CheckAccess confirms the credentials sign in and can see the target's project.
func (a *Adapter) CheckAccess(ctx context.Context, target config.Target) (string, error) {
	client := a.getClient(target.Jira.BaseURL)
	var user currentUser
	if err := get(ctx, client, "/rest/api/3/myself", &user); err != nil {
		return "", fmt.Errorf("sign in to Jira at %s: %w", target.Jira.BaseURL, err)
	}
	found, err := a.project(ctx, target)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("signed in as %s; project %s is reachable", user.DisplayName, found.Key), nil
}

// MissingIssueTypes returns the issue types jira.kind_mapping names that the project lacks.
func (a *Adapter) MissingIssueTypes(ctx context.Context, target config.Target) ([]string, error) {
	if len(target.Jira.KindMapping) == 0 {
		return nil, nil
	}
	return a.missing(ctx, target, slices.Collect(maps.Values(target.Jira.KindMapping)))
}

// missing returns the issue types in wanted that the target's project lacks, sorted.
func (a *Adapter) missing(ctx context.Context, target config.Target, wanted []string) ([]string, error) {
	found, err := a.project(ctx, target)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, name := range provider.UniqueSorted(wanted) {
		if !slices.ContainsFunc(found.IssueTypes, func(t issueType) bool { return strings.EqualFold(t.Name, name) }) {
			missing = append(missing, name)
		}
	}

	return missing, nil
}

// Preflight fails before apply creates anything when the plan creates an issue type the project
// lacks, which Jira would otherwise reject partway through.
func (a *Adapter) Preflight(ctx context.Context, target config.Target, operations []provider.Operation) error {
	var wanted []string
	for _, operation := range operations {
		if issueType, _ := operation.Fields["issue_type"].(string); operation.Action == provider.ActionCreateIssue && issueType != "" {
			wanted = append(wanted, issueType)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	missing, err := a.missing(ctx, target, wanted)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("the Jira project %s has no issue type %s; fix jira.kind_mapping and plan again",
			target.Jira.ProjectKey, strings.Join(missing, ", "))
	}

	return nil
}

// project reads the target's project once per adapter, since doctor checks access and issue
// types in turn.
func (a *Adapter) project(ctx context.Context, target config.Target) (*project, error) {
	key := target.Jira.BaseURL + "/" + target.Jira.ProjectKey
	if found, ok := a.projects[key]; ok {
		return found, nil
	}
	var found project
	if err := get(ctx, a.getClient(target.Jira.BaseURL), "/rest/api/3/project/"+url.PathEscape(target.Jira.ProjectKey), &found); err != nil {
		if errors.Is(err, gerror.ErrNotFound) {
			return nil, fmt.Errorf("the Jira project %s does not exist, or these credentials cannot see it", target.Jira.ProjectKey)
		}

		return nil, fmt.Errorf("read Jira project %s: %w", target.Jira.ProjectKey, err)
	}
	if a.projects == nil {
		a.projects = map[string]*project{}
	}
	a.projects[key] = &found

	return &found, nil
}

func get(ctx context.Context, client *jiraclient.Client, path string, into any) error {
	request, err := client.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}

	return client.Do(request, into)
}
