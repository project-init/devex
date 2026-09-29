package release

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/manifoldco/promptui"
	"github.com/project-init/devex/internal/githubclient"
)

// errGitHubAuthUnavailable is returned when neither GITHUB_TOKEN nor gh CLI auth is available.
var errGitHubAuthUnavailable = errors.New("github auth unavailable")

// pullRequestSummary is a minimal PR record for release preview output.
type pullRequestSummary struct {
	Number int
	Title  string
	Author string
	URL    string
}

// createAndPushTag creates an annotated git tag locally and pushes it to origin.
func createAndPushTag(tag string) error {
	if err := runGit("tag", "-a", tag, "-m", "Release "+tag); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if err := runGit("push", "origin", tag); err != nil {
		return fmt.Errorf("failed to push tag: %w", err)
	}

	return nil
}

// runGit runs a git subprocess with stdout/stderr discarded.
func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// confirmRelease prompts the user to confirm creating and pushing the release tag.
func confirmRelease(v version) error {
	prompt := promptui.Prompt{
		Label:     fmt.Sprintf("Create and push tag %s", v),
		IsConfirm: true,
	}

	_, err := prompt.Run()
	return err
}

// getActionsURL returns the GitHub Actions tab URL for the origin repository.
func getActionsURL() (string, error) {
	owner, repo, err := getOriginGitHubRepo()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://github.com/%s/%s/actions", owner, repo), nil
}

// getOriginGitHubRepo parses owner and repo name from the git origin remote URL.
func getOriginGitHubRepo() (owner, repo string, err error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	remote := strings.TrimSpace(string(out))
	remote = strings.TrimSuffix(remote, ".git")

	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "https://github.com/"):
		path = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "http://github.com/"):
		path = strings.TrimPrefix(remote, "http://github.com/")
	default:
		return "", "", fmt.Errorf("unrecognized GitHub remote format: %s", remote)
	}

	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid GitHub remote: %s", remote)
	}
	return parts[0], parts[1], nil
}

// latestReleaseTagRef returns the most recent git tag reachable from HEAD, if any.
func latestReleaseTagRef() (string, bool) {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// tagCommitTime returns the commit timestamp (RFC3339) for a tag or other git ref.
func tagCommitTime(ref string) (time.Time, error) {
	out, err := exec.Command("git", "log", "-1", "--format=%cI", ref).Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("commit time for %q: %w", ref, err)
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse commit time for %q: %w", ref, err)
	}
	return t, nil
}

// releaseBaseBranch is the target branch for merged PRs shown in the release preview.
const releaseBaseBranch = "main"

// requireGitHubReleaseReady ensures GitHub auth and a valid github.com origin before release steps.
func requireGitHubReleaseReady() error {
	if _, err := resolveGitHubToken(); err != nil {
		if errors.Is(err, errGitHubAuthUnavailable) {
			return fmt.Errorf("GitHub auth required (run `gh auth login` or set GITHUB_TOKEN)")
		}
		return err
	}
	if _, _, err := getOriginGitHubRepo(); err != nil {
		return fmt.Errorf("GitHub origin required: %w", err)
	}
	return nil
}

// displayReleasePullRequests prints merged PRs into main since the latest tag (or all if untagged).
func displayReleasePullRequests(ctx context.Context) error {
	owner, repo, err := getOriginGitHubRepo()
	if err != nil {
		return err
	}

	var since time.Time
	var heading string
	if baselineTag, ok := latestReleaseTagRef(); ok {
		since, err = tagCommitTime(baselineTag)
		if err != nil {
			return err
		}
		heading = fmt.Sprintf("Merged pull requests to %s since %s", releaseBaseBranch, baselineTag)
	} else {
		heading = fmt.Sprintf("Merged pull requests to %s (all history)", releaseBaseBranch)
	}

	prs, err := listMergedPullRequestsSince(ctx, owner, repo, since)
	if err != nil {
		return err
	}

	fmt.Printf("\n%s (%d):\n", heading, len(prs))
	if len(prs) == 0 {
		fmt.Println("  (none)")
		return nil
	}
	for _, pr := range prs {
		fmt.Printf("  #%d %s (@%s)\n", pr.Number, pr.Title, pr.Author)
		fmt.Printf("      %s\n", pr.URL)
	}
	return nil
}

// resolveGitHubToken reads GITHUB_TOKEN from the environment or from gh auth token.
func resolveGitHubToken() (string, error) {
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token, nil
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", errGitHubAuthUnavailable
	}
	if token := strings.TrimSpace(string(out)); token != "" {
		return token, nil
	}
	return "", errGitHubAuthUnavailable
}

// listMergedPullRequestsSince fetches closed PRs merged into main after since via the GitHub API.
func listMergedPullRequestsSince(ctx context.Context, owner, repo string, since time.Time) ([]pullRequestSummary, error) {
	token, err := resolveGitHubToken()
	if err != nil {
		return nil, err
	}

	client, err := githubclient.New(token, "")
	if err != nil {
		return nil, err
	}

	found := make([]pullRequestSummary, 0)
	for page := 1; ; page++ {
		prs, resp, err := client.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
			State:     "closed",
			Sort:      "created",
			Direction: "desc",
			ListOptions: github.ListOptions{Page: page, PerPage: 100},
		})
		if err != nil {
			return nil, fmt.Errorf("list pull requests: %w", err)
		}
		for _, pr := range prs {
			if pr.GetBase().GetRef() != releaseBaseBranch {
				continue
			}
			if pr.MergedAt == nil || !pr.MergedAt.After(since) {
				continue
			}
			found = append(found, pullRequestSummary{
				Number: pr.GetNumber(),
				Title:  pr.GetTitle(),
				Author: pr.GetUser().GetLogin(),
				URL:    pr.GetHTMLURL(),
			})
		}
		if len(prs) == 0 || resp.NextPage == 0 {
			break
		}
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Number < found[j].Number })
	return found, nil
}
