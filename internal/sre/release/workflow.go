package release

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/google/go-github/v74/github"
)

// errNoWorkflowRun is returned when the tag push starts no GitHub Actions run in time.
var errNoWorkflowRun = errors.New("no workflow run started for the tag")

// waitTiming paces waitForWorkflow; tests shorten it.
type waitTiming struct {
	poll    time.Duration // first wait between checks; it doubles up to maxPoll
	maxPoll time.Duration
	appear  time.Duration // how long a run may take to show up
	timeout time.Duration // how long the runs may take to finish
}

var defaultWaitTiming = waitTiming{poll: 3 * time.Second, maxPoll: 30 * time.Second, appear: 90 * time.Second, timeout: 30 * time.Minute}

// workflowResult is a finished run the tag started.
type workflowResult struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// clockSkew allows for the local clock running ahead of GitHub's when filtering runs by start time.
const clockSkew = 2 * time.Minute

// workflowPageURL is the Actions page for one workflow file, such as
// https://github.com/owner/repo/actions/workflows/release.yaml. GitHub keys that page by the
// file name. An empty path returns an empty string.
func workflowPageURL(owner, repo, workflowPath string) string {
	file := path.Base(workflowPath)
	if workflowPath == "" || file == "." || file == "/" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s", owner, repo, url.PathEscape(file))
}

// waitForWorkflow polls GitHub Actions until every run that the push of tag at sha started has
// finished, and returns them. A tag push reports the tag as the run's head branch. Runs created
// before pushedAt belong to an earlier push of the same tag and are ignored. Results must hold
// steady across two polls, so a run that registers late is still waited on. timing.timeout
// bounds the whole wait, including retries inside a single request. notify, when set, is called
// once per distinct workflow page as soon as a matching run is listed, including a run that is
// still in progress.
func waitForWorkflow(ctx context.Context, client *github.Client, owner, repo, tag, sha string, pushedAt time.Time, timing waitTiming, notify func(pageURL string)) ([]workflowResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timing.timeout)
	defer cancel()
	start := time.Now()
	poll := timing.poll
	since := pushedAt.Add(-clockSkew).UTC()
	previous := -1
	seen := make(map[string]struct{})
	for {
		runs, _, err := client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, &github.ListWorkflowRunsOptions{
			Event:   "push",
			Branch:  tag,
			HeadSHA: sha,
			Created: ">=" + since.Format(time.RFC3339),
		})
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("the release workflow is still running after %s", timing.timeout)
		}
		if err != nil {
			return nil, fmt.Errorf("list workflow runs: %w", err)
		}
		if notify != nil {
			for _, page := range newWorkflowPages(runs.WorkflowRuns, owner, repo, tag, since, seen) {
				notify(page)
			}
		}
		results, pending := finishedRuns(runs.WorkflowRuns, tag, since)
		elapsed := time.Since(start)
		switch {
		case len(results) > 0 && !pending && len(results) == previous:
			return results, nil
		case len(results) == 0 && !pending && elapsed > timing.appear:
			return nil, errNoWorkflowRun
		}
		if !pending {
			previous = len(results)
		} else {
			previous = -1
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("the release workflow is still running after %s", timing.timeout)
			}
			return nil, ctx.Err()
		case <-time.After(poll):
		}
		poll = min(poll*2, max(timing.maxPoll, timing.poll))
	}
}

// newWorkflowPages returns workflow pages for matching runs that seen does not already hold,
// and records them there.
func newWorkflowPages(runs []*github.WorkflowRun, owner, repo, tag string, since time.Time, seen map[string]struct{}) []string {
	var pages []string
	for _, run := range runs {
		if !runMatchesRelease(run, tag, since) {
			continue
		}
		page := workflowPageURL(owner, repo, run.GetPath())
		if page == "" {
			continue
		}
		if _, ok := seen[page]; ok {
			continue
		}
		seen[page] = struct{}{}
		pages = append(pages, page)
	}
	return pages
}

// runMatchesRelease reports a run started by this tag push. A created time before since belongs
// to an earlier push of the same tag.
func runMatchesRelease(run *github.WorkflowRun, tag string, since time.Time) bool {
	return run.GetHeadBranch() == tag && !run.GetCreatedAt().Before(since)
}

// finishedRuns returns the completed runs for tag created since since, and whether any is still
// going.
func finishedRuns(runs []*github.WorkflowRun, tag string, since time.Time) ([]workflowResult, bool) {
	var results []workflowResult
	pending := false
	for _, run := range runs {
		if !runMatchesRelease(run, tag, since) {
			continue
		}
		if run.GetStatus() != "completed" {
			pending = true
			continue
		}
		results = append(results, workflowResult{Name: run.GetName(), Conclusion: run.GetConclusion(), URL: run.GetHTMLURL()})
	}
	return results, pending
}
