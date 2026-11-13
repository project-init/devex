package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v74/github"
	"github.com/manifoldco/promptui"
	"github.com/project-init/gommon/pkg/githubclient"
)

// pullRequestSuffix matches the squash-merge subject ending GitHub adds, such as " (#123)".
var pullRequestSuffix = regexp.MustCompile(`\(#(\d+)\)\s*$`)

// releaseBaseBranch is the branch whose fetched tip is tagged and listed.
const releaseBaseBranch = "main"

const (
	releasePRColWidth     = 8
	releaseAuthorColWidth = 16
	releaseTitleMaxRunes  = 56
	shortSHALen           = 7
)

// pullRequestSummary is a minimal PR record for release preview output.
type pullRequestSummary struct {
	Number int
	Title  string
	Author string
	URL    string
}

// releaseCommit is one first-parent commit between the previous tag and origin/main.
type releaseCommit struct {
	SHA     string
	Author  string
	Subject string
	Number  int
}

// releaseLine is one preview row. Ref is "#N" for a pull request and a short SHA otherwise.
type releaseLine struct {
	Ref    string
	Author string
	Title  string
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLPullRequests struct {
	Data struct {
		Repository map[string]*graphQLPullRequest `json:"repository"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// graphQLError is one entry from a GraphQL errors array. Path elements are strings or indexes.
type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

type graphQLPullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

// createAndPushTag creates an annotated git tag on commit and pushes it to origin.
// A failed push deletes that local tag. Otherwise the next release describes it as the
// current version and skips the number that never reached origin.
func createAndPushTag(tag, commit string) error {
	if err := runGit("tag", "-a", tag, "-m", "Release "+tag, commit); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if err := runGit("push", "origin", tag); err != nil {
		if delErr := runGit("tag", "-d", tag); delErr != nil {
			return fmt.Errorf("failed to push tag: %w (also failed to delete local tag: %v)", err, delErr)
		}
		return fmt.Errorf("failed to push tag: %w", err)
	}

	return nil
}

// gitOutput runs git and returns stdout. A non-zero exit includes stderr in the error.
func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", gitCommandError(args, err, stderr.Bytes())
	}
	return stdout.String(), nil
}

// runGit runs a git command, discarding stdout. Failures include git's stderr.
func runGit(args ...string) error {
	_, err := gitOutput(args...)
	return err
}

func gitCommandError(args []string, err error, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	command := strings.Join(args, " ")
	if msg == "" {
		return fmt.Errorf("git %s: %w", command, err)
	}
	return fmt.Errorf("git %s: %w: %s", command, err, msg)
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

// cancelledRelease is the error returned when the user declines the tag.
// promptui.ErrAbort's text is empty, so wrapping it would print "tag creation cancelled:".
func cancelledRelease(err error) error {
	if errors.Is(err, promptui.ErrAbort) || errors.Is(err, promptui.ErrInterrupt) {
		return errors.New("tag creation cancelled")
	}
	if err == nil || err.Error() == "" {
		return errors.New("tag creation cancelled")
	}
	return fmt.Errorf("tag creation cancelled: %w", err)
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
	out, err := gitOutput("remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	remote := strings.TrimSpace(out)
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

// requireGitHubOrigin ensures origin is a github.com remote before release steps.
func requireGitHubOrigin() error {
	if _, _, err := getOriginGitHubRepo(); err != nil {
		return fmt.Errorf("GitHub origin required: %w", err)
	}
	return nil
}

// fetchOriginMain updates origin/main and returns that tip's SHA.
// The release tags this SHA, so a stale checkout is not what gets released.
// Tags that point at the fetched history come along with the branch. Asking for every
// remote tag would fail the command when one local tag would be overwritten, after
// origin/main had already moved.
func fetchOriginMain() (string, error) {
	if err := runGit("fetch", "origin", releaseBaseBranch); err != nil {
		return "", fmt.Errorf("failed to fetch origin/main: %w", err)
	}
	out, err := gitOutput("rev-parse", "origin/"+releaseBaseBranch)
	if err != nil {
		return "", fmt.Errorf("failed to resolve origin/main: %w", err)
	}
	tip := strings.TrimSpace(out)
	if tip == "" {
		return "", errors.New("origin/main resolved to an empty revision")
	}
	return tip, nil
}

// commitsSince lists first-parent commits reachable from tip and not from baseline.
// An empty baseline lists the full first-parent history of tip, newest first.
func commitsSince(baseline, tip string) ([]releaseCommit, error) {
	args := []string{"log", "--first-parent", "-z", "--pretty=tformat:%H%x1f%an%x1f%s"}
	if baseline != "" {
		args = append(args, baseline+".."+tip)
	} else {
		args = append(args, tip)
	}
	out, err := gitOutput(args...)
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}
	return parseCommitLog(out)
}

func parseCommitLog(out string) ([]releaseCommit, error) {
	out = strings.TrimRight(out, "\x00")
	if out == "" {
		return nil, nil
	}
	records := strings.Split(out, "\x00")
	commits := make([]releaseCommit, 0, len(records))
	for _, rec := range records {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 3)
		if len(parts) != 3 || parts[0] == "" {
			return nil, fmt.Errorf("unexpected git log record %q", rec)
		}
		commit := releaseCommit{SHA: parts[0], Author: parts[1], Subject: parts[2]}
		if n, ok := parsePullRequestNumber(commit.Subject); ok {
			commit.Number = n
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// parsePullRequestNumber reads a squash-merge subject suffix such as "(#123)".
func parsePullRequestNumber(subject string) (int, bool) {
	match := pullRequestSuffix.FindStringSubmatch(subject)
	if match == nil {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// displayReleaseCommits prints commits on origin/main since baseline (or all of them).
func displayReleaseCommits(ctx context.Context, token, tip, baseline string) error {
	var heading string
	if baseline != "" {
		heading = fmt.Sprintf("Commits on %s since %s", releaseBaseBranch, baseline)
	} else {
		heading = fmt.Sprintf("Commits on %s (all history)", releaseBaseBranch)
	}

	commits, err := commitsSince(baseline, tip)
	if err != nil {
		return err
	}
	lines, err := loadReleaseLines(ctx, token, commits)
	if err != nil {
		return err
	}

	fmt.Printf("\n%s (%d):\n", heading, len(lines))
	if len(lines) == 0 {
		fmt.Println("  (none)")
		return nil
	}
	printReleaseLines(lines)
	return nil
}

func loadReleaseLines(ctx context.Context, token string, commits []releaseCommit) ([]releaseLine, error) {
	numbers := pullRequestNumbers(commits)
	if len(numbers) == 0 {
		return releaseLines(commits, nil), nil
	}

	owner, repo, err := getOriginGitHubRepo()
	if err != nil {
		return nil, err
	}
	client, err := githubclient.New(token, "")
	if err != nil {
		return nil, err
	}
	prs, err := fetchPullRequestSummaries(ctx, client, owner, repo, numbers)
	if err != nil {
		return nil, err
	}
	return releaseLines(commits, prs), nil
}

func pullRequestNumbers(commits []releaseCommit) []int {
	numbers := make([]int, 0)
	for _, commit := range commits {
		if commit.Number > 0 {
			numbers = append(numbers, commit.Number)
		}
	}
	return numbers
}

func releaseLines(commits []releaseCommit, prs map[int]pullRequestSummary) []releaseLine {
	lines := make([]releaseLine, 0, len(commits))
	for _, commit := range commits {
		if commit.Number == 0 {
			lines = append(lines, releaseLine{
				Ref:    shortSHA(commit.SHA),
				Author: commit.Author,
				Title:  commit.Subject,
			})
			continue
		}
		pr, ok := prs[commit.Number]
		if !ok {
			lines = append(lines, releaseLine{
				Ref:    fmt.Sprintf("#%d", commit.Number),
				Author: commit.Author,
				Title:  commit.Subject,
			})
			continue
		}
		lines = append(lines, releaseLine{
			Ref:    fmt.Sprintf("#%d", pr.Number),
			Author: pr.Author,
			Title:  pr.Title,
		})
	}
	return lines
}

func shortSHA(sha string) string {
	if len(sha) <= shortSHALen {
		return sha
	}
	return sha[:shortSHALen]
}

func printReleaseLines(lines []releaseLine) {
	fmt.Printf("  %-*s  %-*s  %s\n", releasePRColWidth, "#", releaseAuthorColWidth, "Author", "Title")
	for _, line := range lines {
		fmt.Printf("  %-*s  %-*s  %s\n",
			releasePRColWidth, line.Ref,
			releaseAuthorColWidth, truncateRunes(line.Author, releaseAuthorColWidth),
			truncateRunes(line.Title, releaseTitleMaxRunes),
		)
	}
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(r[:maxRunes-1]) + "…"
}

func githubAuthError(err error) error {
	if errors.Is(err, githubclient.ErrAuthUnavailable) {
		return fmt.Errorf("GitHub auth required (run `gh auth login` or set GITHUB_TOKEN)")
	}
	return err
}

// fetchPullRequestSummaries loads titles and authors for numbers in one GraphQL request.
// No numbers means no request. A null pull request is omitted so the caller can keep the git commit.
func fetchPullRequestSummaries(ctx context.Context, client *github.Client, owner, repo string, numbers []int) (map[int]pullRequestSummary, error) {
	query, ok := pullRequestQuery(numbers)
	if !ok {
		return map[int]pullRequestSummary{}, nil
	}

	req, err := client.NewRequest("POST", "graphql", graphQLRequest{
		Query: query,
		Variables: map[string]any{
			"owner": owner,
			"name":  repo,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fetch pull requests: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	var payload graphQLPullRequests
	if _, err := client.Do(ctx, req, &payload); err != nil {
		return nil, fmt.Errorf("fetch pull requests: %w", err)
	}
	if payload.Data.Repository == nil {
		return nil, graphQLFailure(payload.Errors, "github graphql returned no repository")
	}
	if msgs := fatalGraphQLErrors(payload.Errors); len(msgs) > 0 {
		return nil, fmt.Errorf("fetch pull requests: %s", strings.Join(msgs, "; "))
	}

	found := make(map[int]pullRequestSummary)
	for _, node := range payload.Data.Repository {
		if node == nil || node.Number <= 0 {
			continue
		}
		author := ""
		if node.Author != nil {
			author = node.Author.Login
		}
		found[node.Number] = pullRequestSummary{
			Number: node.Number,
			Title:  node.Title,
			Author: author,
			URL:    node.URL,
		}
	}
	return found, nil
}

// fatalGraphQLErrors keeps errors that are not a single missing pull request.
// GitHub returns HTTP 200 with the alias set to null and a NOT_FOUND error when a
// subject suffix is not a pull request in this repository. Those commits stay in the list.
func fatalGraphQLErrors(errs []graphQLError) []string {
	msgs := make([]string, 0)
	for _, e := range errs {
		if e.missingPullRequest() {
			continue
		}
		if e.Message != "" {
			msgs = append(msgs, e.Message)
			continue
		}
		msgs = append(msgs, "github graphql error")
	}
	return msgs
}

func graphQLFailure(errs []graphQLError, fallback string) error {
	if msgs := fatalGraphQLErrors(errs); len(msgs) > 0 {
		return fmt.Errorf("fetch pull requests: %s", strings.Join(msgs, "; "))
	}
	return fmt.Errorf("fetch pull requests: %s", fallback)
}

// missingPullRequest reports a NOT_FOUND aimed at one pullRequest alias.
// A missing repository has a shorter path and must still fail the preview.
func (e graphQLError) missingPullRequest() bool {
	if len(e.Path) < 2 {
		return false
	}
	head, ok := e.Path[0].(string)
	if !ok || head != "repository" {
		return false
	}
	if strings.EqualFold(e.Type, "NOT_FOUND") {
		return true
	}
	return strings.Contains(e.Message, "Could not resolve to a PullRequest")
}

// pullRequestQuery builds one GraphQL query for the distinct positive PR numbers.
func pullRequestQuery(numbers []int) (string, bool) {
	seen := make(map[int]struct{}, len(numbers))
	var b strings.Builder
	b.WriteString("query ($owner: String!, $name: String!) {\n")
	b.WriteString("  repository(owner: $owner, name: $name) {\n")
	wrote := false
	for _, n := range numbers {
		if n <= 0 {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		fmt.Fprintf(&b, "    pr%d: pullRequest(number: %d) { number title url author { login } }\n", n, n)
		wrote = true
	}
	if !wrote {
		return "", false
	}
	b.WriteString("  }\n}\n")
	return b.String(), true
}
