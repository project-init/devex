package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/provider"
	gerror "github.com/project-init/gommon/pkg/errors"
	"github.com/project-init/gommon/pkg/jiraclient"
	"golang.org/x/sync/errgroup"
)

// Confirm reads each candidate issue by key, as many at once as apply runs. Unlike the search
// Resolve runs, a read by key sees an issue the moment Jira creates it. An issue counts only while
// it sits in the target's project and its stamp still names the candidate's idempotency key. Its
// links fill the link cache, as Resolve's search does. A confirmed issue missing a label the search
// filters on gets it back; after the first refusal, Confirm stops trying.
func (a *Adapter) Confirm(ctx context.Context, target config.Target, candidates map[string]provider.RemoteRef) (map[string]provider.RemoteRef, error) {
	client := a.getClient(target.Jira.BaseURL)
	issues := make(map[string]*jiraclient.Issue, len(candidates))
	var mu sync.Mutex
	group, readCtx := errgroup.WithContext(ctx)
	group.SetLimit(a.MaxConcurrent())
	for idempotencyKey, ref := range candidates {
		group.Go(func() error {
			issue, err := client.GetIssue(readCtx, ref.Key, []string{"project", "labels", "issuelinks"}, []string{propertyKey})
			// Jira answers 404 for an issue this token cannot see as well as for one deleted. Any
			// other failure, a 403 included, says nothing about the issue, so it stops apply rather
			// than risk a second copy.
			if errors.Is(err, gerror.ErrNotFound) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("read Jira issue %s: %w", ref.Key, err)
			}
			var project struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(issue.Fields["project"], &project); err != nil {
				return fmt.Errorf("decode the project of Jira issue %s: %w", issue.Key, err)
			}
			stamp, err := readStamp(*issue)
			if err != nil {
				return err
			}
			// Jira matches project keys without regard to case, as Resolve's search does.
			if !strings.EqualFold(project.Key, target.Jira.ProjectKey) || stamp.ID != idempotencyKey {
				return nil
			}
			links, err := issue.Links()
			if err != nil {
				return err
			}
			a.links.store(issue.Key, links)
			mu.Lock()
			issues[idempotencyKey] = issue
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	confirmed := make(map[string]provider.RemoteRef, len(issues))
	labeling := true
	for _, idempotencyKey := range slices.Sorted(maps.Keys(issues)) {
		issue := issues[idempotencyKey]
		if labeling && restoreLabels(ctx, client, issue, idempotencyKey) != nil {
			labeling = false
		}
		confirmed[idempotencyKey] = issueRef(target, issue.ID, issue.Key)
	}
	return confirmed, nil
}

// restoreLabels adds back the generated and discovery labels Resolve's search filters on.
func restoreLabels(ctx context.Context, client *jiraclient.Client, issue *jiraclient.Issue, idempotencyKey string) error {
	var labels []string
	_ = json.Unmarshal(issue.Fields["labels"], &labels)
	discoveryID, _, _ := strings.Cut(idempotencyKey, "/")
	var add []map[string]string
	for _, label := range []string{generatedLabel, discoveryID} {
		if label != "" && !slices.Contains(labels, label) {
			add = append(add, map[string]string{"add": label})
		}
	}
	if len(add) == 0 {
		return nil
	}
	return client.UpdateIssue(ctx, issue.Key, jiraclient.IssueUpdate{Update: map[string]any{"labels": add}})
}
