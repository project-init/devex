package release

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manifoldco/promptui"
	"github.com/project-init/gommon/pkg/githubclient"
)

func TestParsePullRequestNumber(t *testing.T) {
	cases := []struct {
		subject string
		number  int
		ok      bool
	}{
		{subject: "feat: add thing (#111)", number: 111, ok: true},
		{subject: "feat: add thing (#111)  ", number: 111, ok: true},
		{subject: "(#42)", number: 42, ok: true},
		{subject: "Merge pull request #111 from org/branch", ok: false},
		{subject: "fix (#12) more text", ok: false},
		{subject: "(#0)", ok: false},
		{subject: "subject (#)", ok: false},
		{subject: "overflow (#999999999999999999999)", ok: false},
	}
	for _, tc := range cases {
		got, ok := parsePullRequestNumber(tc.subject)
		if ok != tc.ok || got != tc.number {
			t.Errorf("parsePullRequestNumber(%q) = %d, %v; want %d, %v", tc.subject, got, ok, tc.number, tc.ok)
		}
	}
}

func TestReleaseLinesKeepOrderAndPlainCommits(t *testing.T) {
	commits := []releaseCommit{
		{SHA: "abcdef1234567890", Author: "Ada", Subject: "plain commit", Number: 0},
		{SHA: "1234567abcdef", Author: "Git Author", Subject: "feat: ship it (#15)", Number: 15},
		{SHA: "fedcba9876543210", Author: "Grace", Subject: "gone (#9)", Number: 9},
	}
	prs := map[int]pullRequestSummary{
		15: {Number: 15, Title: "Ship it from API", Author: "octocat"},
	}

	lines := releaseLines(commits, prs)
	want := []releaseLine{
		{Ref: "abcdef1", Author: "Ada", Title: "plain commit"},
		{Ref: "#15", Author: "octocat", Title: "Ship it from API"},
		{Ref: "#9", Author: "Grace", Title: "gone (#9)"},
	}
	if len(lines) != len(want) {
		t.Fatalf("len = %d, want %d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

func TestCommitsSinceFirstParentRange(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	commitFile(t, dir, "base", "base\n", "base")
	gitRun(t, dir, "tag", "v0.1.0")
	gitRun(t, dir, "checkout", "-b", "side")
	commitFile(t, dir, "side", "side\n", "side only")
	gitRun(t, dir, "checkout", "main")
	commitFile(t, dir, "plain", "plain\n", "plain commit without a pull request")
	commitFile(t, dir, "pr", "pr\n", "feat: ship it (#15)")
	gitRun(t, dir, "merge", "--no-ff", "-m", "Merge branch 'side'", "side")
	tip := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))

	t.Chdir(dir)
	commits, err := commitsSince("v0.1.0", tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %+v, want 3 first-parent commits", commits)
	}
	if commits[0].Subject != "Merge branch 'side'" || commits[0].Number != 0 {
		t.Errorf("newest = %+v", commits[0])
	}
	if commits[1].Number != 15 || commits[1].Subject != "feat: ship it (#15)" {
		t.Errorf("pull request commit = %+v", commits[1])
	}
	if commits[2].Subject != "plain commit without a pull request" || commits[2].Number != 0 {
		t.Errorf("plain commit = %+v", commits[2])
	}
	for _, commit := range commits {
		if commit.Subject == "side only" || commit.Subject == "base" {
			t.Errorf("commit %q is outside the first-parent range", commit.Subject)
		}
	}
}

func TestFetchPullRequestSummariesOneRequest(t *testing.T) {
	var calls int
	var gotPath, gotMethod string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		body = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":{
			"pr111":{"number":111,"title":"From API","url":"https://example/111","author":{"login":"octocat"}},
			"pr222":null,
			"pr333":{"number":333,"title":"No author","url":"https://example/333","author":null}
		}},"errors":[{"type":"NOT_FOUND","path":["repository","pr222"],"message":"Could not resolve to a PullRequest with the number of 222."}]}`)
	}))
	t.Cleanup(server.Close)

	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchPullRequestSummaries(context.Background(), client, "acme", "widgets", []int{111, 222, 111, 333})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("requests = %d, want 1", calls)
	}
	if gotMethod != http.MethodPost || gotPath != "/graphql" {
		t.Fatalf("request = %s %s, want POST /graphql", gotMethod, gotPath)
	}

	var payload struct {
		Query     string `json:"query"`
		Variables struct {
			Owner string `json:"owner"`
			Name  string `json:"name"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request: %v\n%s", err, body)
	}
	if payload.Variables.Owner != "acme" || payload.Variables.Name != "widgets" {
		t.Errorf("variables = %+v", payload.Variables)
	}
	if strings.Count(payload.Query, "pr111:") != 1 || strings.Count(payload.Query, "pr222:") != 1 || strings.Count(payload.Query, "pr333:") != 1 {
		t.Errorf("query = %s", payload.Query)
	}

	if got[111].Title != "From API" || got[111].Author != "octocat" || got[111].URL != "https://example/111" {
		t.Errorf("pr 111 = %+v", got[111])
	}
	if _, ok := got[222]; ok {
		t.Fatal("null pull request was included")
	}
	if got[333].Title != "No author" || got[333].Author != "" {
		t.Errorf("pr 333 = %+v", got[333])
	}
}

func TestFetchPullRequestSummariesSkipsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(server.Close)

	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchPullRequestSummaries(context.Background(), client, "acme", "widgets", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %#v, want none", got)
	}
}

func TestFetchPullRequestSummariesReportsGraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":[{"message":"Something went wrong"}]}`)
	}))
	t.Cleanup(server.Close)

	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchPullRequestSummaries(context.Background(), client, "acme", "widgets", []int{4})
	if err == nil || !strings.Contains(err.Error(), "Something went wrong") {
		t.Fatalf("error = %v, want the graphql message", err)
	}
}

func TestFetchPullRequestSummariesReportsPartialGraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":{"pr4":{"number":4,"title":"Kept","url":"https://example/4","author":{"login":"octocat"}}}},"errors":[{"message":"Something went wrong while executing your query"}]}`)
	}))
	t.Cleanup(server.Close)

	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchPullRequestSummaries(context.Background(), client, "acme", "widgets", []int{4})
	if err == nil || !strings.Contains(err.Error(), "Something went wrong while executing your query") {
		t.Fatalf("error = %v, want the partial failure", err)
	}
}

func TestFetchPullRequestSummariesReportsMissingRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"message":"Could not resolve to a Repository with the name widgets."}]}`)
	}))
	t.Cleanup(server.Close)

	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchPullRequestSummaries(context.Background(), client, "acme", "widgets", []int{4})
	if err == nil || !strings.Contains(err.Error(), "Could not resolve to a Repository") {
		t.Fatalf("error = %v, want the missing repository", err)
	}
}

func TestVersionAtTipUsesFirstParentTag(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	commitFile(t, dir, "base", "base\n", "base")
	gitRun(t, dir, "tag", "v1.2.3")
	gitRun(t, dir, "checkout", "-b", "other")
	commitFile(t, dir, "other", "other\n", "other")
	gitRun(t, dir, "tag", "v9.9.9")
	gitRun(t, dir, "checkout", "main")
	commitFile(t, dir, "mainline", "main\n", "main only")
	gitRun(t, dir, "merge", "--no-ff", "-m", "Merge branch 'other'", "other")
	tip := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))

	t.Chdir(dir)
	got, baseline, err := versionAtTip(tip)
	if err != nil {
		t.Fatal(err)
	}
	if baseline != "v1.2.3" || got.String() != "v1.2.3" {
		t.Fatalf("version = %s baseline %q, want v1.2.3", got, baseline)
	}
}

func TestVersionAtTipReturnsDescribeErrors(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	commitFile(t, dir, "f", "a\n", "base")
	gitRun(t, dir, "tag", "v1.0.0")

	t.Chdir(dir)
	_, _, err := versionAtTip("deadbeefdead")
	if err == nil || !strings.Contains(err.Error(), "Not a valid object name") {
		t.Fatalf("error = %v, want git's describe message", err)
	}
}

func TestVersionAtTipWithoutTags(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	sha := commitFile(t, dir, "f", "a\n", "base")

	t.Chdir(dir)
	got, baseline, err := versionAtTip(sha)
	if err != nil {
		t.Fatal(err)
	}
	if baseline != "" || got != (version{}) {
		t.Fatalf("version = %s baseline %q, want v0.0.0 and no tag", got, baseline)
	}
}

func TestCreateAndPushTagUsesFetchedOriginMain(t *testing.T) {
	bare := t.TempDir()
	work := t.TempDir()
	gitRun(t, bare, "init", "--bare", "-b", "main")
	initRepo(t, work)
	old := commitFile(t, work, "f", "a\n", "base")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "push", "origin", "main")
	tip := commitFile(t, work, "f", "b\n", "feat: next (#2)")
	gitRun(t, work, "push", "origin", "main")
	gitRun(t, work, "reset", "--hard", old)
	gitRun(t, work, "update-ref", "refs/remotes/origin/main", old)

	t.Chdir(work)
	got, err := fetchOriginMain()
	if err != nil {
		t.Fatal(err)
	}
	if got != tip {
		t.Fatalf("fetchOriginMain = %s, want %s", got, tip)
	}
	head := strings.TrimSpace(gitRun(t, work, "rev-parse", "HEAD"))
	if head == tip {
		t.Fatal("local checkout moved to origin/main")
	}
	if err := createAndPushTag("v1.2.3", got); err != nil {
		t.Fatal(err)
	}
	tagged := strings.TrimSpace(gitRun(t, work, "rev-parse", "v1.2.3^{}"))
	if tagged != tip {
		t.Fatalf("tag points at %s, want %s", tagged, tip)
	}
	if headNow := strings.TrimSpace(gitRun(t, work, "rev-parse", "HEAD")); headNow != head {
		t.Fatalf("HEAD changed from %s to %s", head, headNow)
	}
	remote := strings.TrimSpace(gitRun(t, bare, "rev-parse", "v1.2.3^{}"))
	if remote != tip {
		t.Fatalf("pushed tag points at %s, want %s", remote, tip)
	}
}

func TestFetchOriginMainUpdatesTipWhenATagWouldBeClobbered(t *testing.T) {
	bare := t.TempDir()
	work := t.TempDir()
	gitRun(t, bare, "init", "--bare", "-b", "main")
	initRepo(t, work)
	old := commitFile(t, work, "f", "a\n", "base")
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "push", "origin", "main")
	tip := commitFile(t, work, "f", "b\n", "feat: next (#2)")
	gitRun(t, work, "tag", "v0.1.0")
	gitRun(t, work, "push", "origin", "main", "--tags")
	gitRun(t, work, "tag", "-f", "v0.1.0", old)
	gitRun(t, work, "reset", "--hard", old)
	gitRun(t, work, "update-ref", "refs/remotes/origin/main", old)

	t.Chdir(work)
	got, err := fetchOriginMain()
	if err != nil {
		t.Fatal(err)
	}
	if got != tip {
		t.Fatalf("fetchOriginMain = %s, want %s", got, tip)
	}
}

func TestCancelledReleaseHidesEmptyAbort(t *testing.T) {
	for _, err := range []error{promptui.ErrAbort, promptui.ErrInterrupt} {
		got := cancelledRelease(err)
		if got == nil || got.Error() != "tag creation cancelled" {
			t.Errorf("cancelledRelease(%v) = %v", err, got)
		}
	}
}

func TestFailedTagPushIncludesGitStderr(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	commitFile(t, dir, "f", "a\n", "base")
	gitRun(t, dir, "tag", "v1.0.0")
	sha := commitFile(t, dir, "g", "b\n", "next")

	t.Chdir(dir)
	err := createAndPushTag("v1.0.1", sha)
	if err == nil {
		t.Fatal("expected push to fail")
	}
	if !strings.Contains(err.Error(), "failed to push tag") || !strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("error = %q, want git's fatal message", err)
	}
	if cmd := exec.Command("git", "-C", dir, "rev-parse", "v1.0.1"); cmd.Run() == nil {
		t.Fatal("unpushed tag v1.0.1 was left in the repo")
	}
	got, baseline, err := versionAtTip(sha)
	if err != nil {
		t.Fatal(err)
	}
	if baseline != "v1.0.0" || got.String() != "v1.0.0" {
		t.Fatalf("version after failed push = %s baseline %q, want v1.0.0", got, baseline)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "release-test@example.com")
	gitRun(t, dir, "config", "user.name", "Release Test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	gitRun(t, dir, "config", "tag.gpgsign", "false")
}

func commitFile(t *testing.T, dir, name, body, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", name)
	gitRun(t, dir, "commit", "-m", message)
	return strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
