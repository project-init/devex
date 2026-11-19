package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/ui"
	"github.com/project-init/gommon/pkg/githubclient"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		title        string
		wantKind     changeKind
		wantBreaking bool
	}{
		{"feat: add search", kindFeature, false},
		{"feat(api)!: drop v1", kindFeature, true},
		{"fix(INIT-1): handle nil", kindFix, false},
		{"chore: bump deps", kindOther, false},
		{"refactor!: rename package", kindOther, true},
		{"Bump golang.org/x/mod from 0.41.0 to 0.42.0 (#103)", kindOther, false},
		{"fix: parser BREAKING CHANGE: drops v1 keys", kindFix, true},
		{"docs: explain BREAKING CHANGE footers", kindOther, false},
	}
	for _, test := range tests {
		kind, breaking := classify(test.title)
		if kind != test.wantKind || breaking != test.wantBreaking {
			t.Errorf("classify(%q) = %s, %v; want %s, %v", test.title, kind, breaking, test.wantKind, test.wantBreaking)
		}
	}
}

func TestSuggestBump(t *testing.T) {
	lines := func(titles ...string) []releaseLine {
		out := make([]releaseLine, len(titles))
		for i, title := range titles {
			out[i] = newReleaseLine("", "", title)
		}
		return out
	}
	tests := []struct {
		name        string
		lines       []releaseLine
		allowMajor  bool
		want        bumpType
		wantBlocked bool
	}{
		{"fixes only", lines("fix: a", "chore: b"), true, BumpPatch, false},
		{"a feature", lines("fix: a", "feat: b"), true, BumpMinor, false},
		{"breaking allowed", lines("feat: a", "fix!: b"), true, BumpMajor, false},
		{"breaking blocked", lines("fix!: b"), false, BumpMinor, true},
		{"nothing", nil, false, BumpPatch, false},
	}
	for _, test := range tests {
		got, blocked := suggestBump(test.lines, test.allowMajor)
		if got != test.want || blocked != test.wantBlocked {
			t.Errorf("%s: suggestBump() = %s, %v; want %s, %v", test.name, got, blocked, test.want, test.wantBlocked)
		}
	}
}

func TestValidateFlagsWithoutTerminal(t *testing.T) {
	u := ui.NewWithStreams(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{})
	var missing *ui.MissingFlagError
	if _, err := validateFlags(u, flags{}, false); !errors.As(err, &missing) || missing.Flag != bumpFlag {
		t.Errorf("validateFlags() error = %v, want pass --bump", err)
	}
	if _, err := validateFlags(u, flags{bump: "patch"}, false); !errors.As(err, &missing) || missing.Flag != ui.YesFlag {
		t.Errorf("validateFlags(--bump) error = %v, want pass --yes", err)
	}
	if _, err := validateFlags(u, flags{dryRun: true}, false); err != nil {
		t.Errorf("validateFlags(--dry-run) error = %v, want none: a dry run needs no answers", err)
	}
	yes := ui.NewWithStreams(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{Yes: true})
	if got, err := validateFlags(yes, flags{bump: "minor"}, false); err != nil || got != BumpMinor {
		t.Errorf("validateFlags(--bump minor --yes) = %v, %v; want minor", got, err)
	}
}

// workflowRunsFixture is a GET /repos/project-init/devex/actions/runs response recorded from
// the real API, with commit emails redacted.
func workflowRunsFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/workflow_runs.golden")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// respondInOrder serves bodies one per request, repeating the last, and counts the requests.
func respondInOrder(t *testing.T, calls *atomic.Int32, bodies ...string) *github.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := min(int(calls.Add(1))-1, len(bodies)-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[index]))
	}))
	t.Cleanup(server.Close)
	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestWorkflowPageURL(t *testing.T) {
	got := workflowPageURL("project-init", "gommon", ".github/workflows/release.yaml")
	want := "https://github.com/project-init/gommon/actions/workflows/release.yaml"
	if got != want {
		t.Errorf("workflowPageURL() = %q, want %q", got, want)
	}
	if workflowPageURL("o", "r", "") != "" || workflowPageURL("o", "r", ".") != "" {
		t.Error("workflowPageURL() produced a page for an empty path")
	}
	got = workflowPageURL("o", "r", ".github/workflows/my workflow.yaml")
	want = "https://github.com/o/r/actions/workflows/my%20workflow.yaml"
	if got != want {
		t.Errorf("workflowPageURL() = %q, want %q", got, want)
	}
}

func TestWaitForWorkflowPollsUntilComplete(t *testing.T) {
	completed := workflowRunsFixture(t)
	inProgress := strings.Replace(strings.Replace(completed, `"status": "completed"`, `"status": "in_progress"`, 1), `"conclusion": "success"`, `"conclusion": null`, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("event") != "push" || query.Get("branch") != "v0.23.0" || query.Get("head_sha") == "" {
			t.Errorf("query = %v, want event, branch, and head_sha filters", query)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(inProgress))
			return
		}
		_, _ = w.Write([]byte(completed))
	}))
	t.Cleanup(server.Close)
	client, err := githubclient.New("token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var pages []string
	results, err := waitForWorkflow(context.Background(), client, "project-init", "devex", "v0.23.0",
		"055df727ab6d6348493b800ce4a30a5cc75b4c60", time.Time{}, waitTiming{poll: time.Millisecond, appear: time.Second, timeout: time.Second},
		func(pageURL string) { pages = append(pages, pageURL) })
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(results) != 1 || results[0].Conclusion != "success" ||
		results[0].URL != "https://github.com/project-init/devex/actions/runs/43507989316" {
		t.Errorf("results = %+v after %d calls, want the finished Release run, confirmed on the third", results, calls.Load())
	}
	if len(pages) != 1 || pages[0] != "https://github.com/project-init/devex/actions/workflows/release.yaml" {
		t.Errorf("workflow pages = %v, want the release workflow once", pages)
	}
}

func TestWaitForWorkflowIgnoresRunsFromAnEarlierPush(t *testing.T) {
	var calls atomic.Int32
	client := respondInOrder(t, &calls, workflowRunsFixture(t))
	pushedAt := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) // after the fixture run was created
	_, err := waitForWorkflow(context.Background(), client, "project-init", "devex", "v0.23.0", "sha", pushedAt,
		waitTiming{poll: time.Millisecond, appear: 5 * time.Millisecond, timeout: time.Second}, nil)
	if !errors.Is(err, errNoWorkflowRun) {
		t.Errorf("waitForWorkflow() error = %v, want the old run ignored", err)
	}
}

func TestWaitForWorkflowGivesUpWhenNoRunStarts(t *testing.T) {
	var calls atomic.Int32
	client := respondInOrder(t, &calls, `{"total_count":0,"workflow_runs":[]}`)
	_, err := waitForWorkflow(context.Background(), client, "o", "r", "v1.0.0", "abc", time.Time{},
		waitTiming{poll: time.Millisecond, appear: 5 * time.Millisecond, timeout: time.Second}, nil)
	if !errors.Is(err, errNoWorkflowRun) {
		t.Errorf("waitForWorkflow() error = %v, want errNoWorkflowRun", err)
	}
}

// runsResponse builds a runs response from the fixture's run, one copy per given override.
func runsResponse(t *testing.T, overrides ...map[string]any) string {
	t.Helper()
	var fixture struct {
		WorkflowRuns []map[string]any `json:"workflow_runs"`
	}
	if err := json.Unmarshal([]byte(workflowRunsFixture(t)), &fixture); err != nil {
		t.Fatal(err)
	}
	runs := make([]map[string]any, 0, len(overrides))
	for _, override := range overrides {
		run := maps.Clone(fixture.WorkflowRuns[0])
		maps.Copy(run, override)
		runs = append(runs, run)
	}
	data, err := json.Marshal(map[string]any{"total_count": len(runs), "workflow_runs": runs})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWaitForWorkflowWaitsForEveryTagRun(t *testing.T) {
	otherBranch := map[string]any{"id": 3, "head_branch": "main", "status": "in_progress", "conclusion": nil}
	first := runsResponse(t,
		map[string]any{"id": 1, "name": "Release"},
		map[string]any{"id": 2, "name": "Publish", "status": "in_progress", "conclusion": nil},
		otherBranch)
	second := runsResponse(t,
		map[string]any{"id": 1, "name": "Release"},
		map[string]any{"id": 2, "name": "Publish", "conclusion": "failure"},
		otherBranch)
	var calls atomic.Int32
	client := respondInOrder(t, &calls, first, second)
	results, err := waitForWorkflow(context.Background(), client, "project-init", "devex", "v0.23.0", "sha", time.Time{},
		waitTiming{poll: time.Millisecond, appear: time.Second, timeout: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("waitForWorkflow() error = %v", err)
	}
	if len(results) != 2 || results[1].Name != "Publish" || results[1].Conclusion != "failure" {
		t.Errorf("results = %+v, want both tag runs, ignoring the main-branch run", results)
	}
}

func TestWaitForWorkflowNotifiesEachWorkflowPage(t *testing.T) {
	body := runsResponse(t,
		map[string]any{"id": 1, "name": "Release", "path": ".github/workflows/release.yaml"},
		map[string]any{"id": 2, "name": "Publish", "path": ".github/workflows/publish.yaml"},
	)
	var calls atomic.Int32
	client := respondInOrder(t, &calls, body)
	var pages []string
	_, err := waitForWorkflow(context.Background(), client, "project-init", "gommon", "v0.23.0", "sha", time.Time{},
		waitTiming{poll: time.Millisecond, appear: time.Second, timeout: time.Second},
		func(pageURL string) { pages = append(pages, pageURL) })
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/project-init/gommon/actions/workflows/release.yaml\n" +
		"https://github.com/project-init/gommon/actions/workflows/publish.yaml"
	if strings.Join(pages, "\n") != want {
		t.Errorf("workflow pages = %v, want each workflow once", pages)
	}
}

func TestPrintReleaseLinesGroupsByKind(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{})
	printReleaseLines(u, "v1.0.0", []releaseLine{
		newReleaseLine("#3", "ana", "chore: bump deps"),
		newReleaseLine("#2", "bo", "fix: handle nil"),
		newReleaseLine("#1", "cy", "feat!: drop v1"),
	})
	got := out.String()
	features, fixes, other := strings.Index(got, "Features (1)"), strings.Index(got, "Fixes (1)"), strings.Index(got, "Other changes (1)")
	if features < 0 || fixes < features || other < fixes {
		t.Errorf("output groups out of order:\n%s", got)
	}
	if !strings.Contains(got, "BREAKING feat!: drop v1") || !strings.Contains(got, "Commits on main since v1.0.0 (3)") {
		t.Errorf("output = %q, want the breaking marker and the heading", got)
	}
}

// fakeSession answers validateFlags' questions about the terminal and flags.
type fakeSession struct{ interactive, yes, json bool }

func (s fakeSession) Interactive() bool { return s.interactive }
func (s fakeSession) Yes() bool         { return s.yes }
func (s fakeSession) JSON() bool        { return s.json }

func TestValidateFlagsJSONNeedsBump(t *testing.T) {
	terminalJSON := fakeSession{interactive: true, json: true}
	var missing *ui.MissingFlagError
	if _, err := validateFlags(terminalJSON, flags{}, false); !errors.As(err, &missing) || missing.Flag != bumpFlag {
		t.Errorf("validateFlags(json on a terminal) error = %v, want pass --bump", err)
	}
	if _, err := validateFlags(terminalJSON, flags{bump: "patch"}, false); !errors.As(err, &missing) || missing.Flag != ui.YesFlag {
		t.Errorf("validateFlags(json --bump on a terminal) error = %v, want pass --yes", err)
	}
	if _, err := validateFlags(terminalJSON, flags{dryRun: true}, false); err != nil {
		t.Errorf("validateFlags(json, --dry-run) error = %v, want none", err)
	}
	if _, err := validateFlags(fakeSession{interactive: true}, flags{}, false); err != nil {
		t.Errorf("validateFlags(text on a terminal) error = %v, want none: it can prompt", err)
	}
}

func TestFailedConclusions(t *testing.T) {
	for conclusion, failed := range map[string]bool{"success": false, "skipped": false, "neutral": false, "failure": true, "cancelled": true, "timed_out": true} {
		if failedConclusions[conclusion] != failed {
			t.Errorf("failedConclusions[%q] = %v, want %v", conclusion, failedConclusions[conclusion], failed)
		}
	}
}
