package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/doctor"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/provider/jira"
	"github.com/project-init/devex/internal/discovery/publish"
	"github.com/project-init/devex/internal/ui"
)

func TestPrintPlanGroupsByActionWithFieldChanges(t *testing.T) {
	update := provider.Operation{Action: provider.ActionUpdateIssue, Summary: "Update Jira DEVEX-2 for WI-001: summary, description", Diff: map[string]provider.FieldChange{
		"summary":     {Before: "Audit logs", After: "Ship audit logs"},
		"description": provider.Rewritten,
	}}
	plan := &provider.Plan{Operations: []provider.Operation{
		{Action: provider.ActionLinkIssues, Summary: "Link WI-001 to WI-002"},
		{Action: provider.ActionCreateIssue, Summary: "Create Story for WI-003"},
		update,
		{Action: provider.ActionCreateIssue, Summary: "Create Task for WI-004"},
	}}
	var out bytes.Buffer
	printPlan(ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{}), "plan.yaml", plan)
	want := `Publication plan: plan.yaml

Create (2)
  - Create Story for WI-003
  - Create Task for WI-004

Update (1)
  - Update Jira DEVEX-2 for WI-001: summary, description
      description: rewritten from the bundle
      summary: "Audit logs" → "Ship audit logs"

Link (1)
  - Link WI-001 to WI-002
`
	if got := out.String(); got != want {
		t.Errorf("printPlan() =\n%s\nwant\n%s", got, want)
	}
}

func TestSummaryCountsEveryOutcome(t *testing.T) {
	plan := &provider.Plan{Operations: []provider.Operation{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}}
	receipt := &provider.Receipt{Operations: map[string]provider.OperationResult{
		"a": {Status: "created"}, "b": {Status: "reused"}, "c": {Status: "failed", Error: "boom"},
	}}
	outcomes := []publish.Progress{{Result: receipt.Operations["a"], Resumed: true}, {Result: receipt.Operations["b"]}, {Result: receipt.Operations["c"]}}
	if got := summarize(plan, receipt, outcomes).String(); got != "Applied: 1 created, 0 updated, 1 reused, 1 failed, 2 not run (1 from an earlier run)" {
		t.Errorf("summary = %q", got)
	}
	// Stopped before the loop, as on a failed preflight: settled work still counts.
	if got := summarize(plan, receipt, nil).String(); got != "Applied: 1 created, 0 updated, 1 reused, 1 failed, 2 not run" {
		t.Errorf("summary without progress = %q", got)
	}
}

// writeJiraConfig writes a target configuration whose Jira target maps features to Story.
func writeJiraConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "discovery.yaml")
	body := "targets:\n  jira:\n    provider: jira\n    jira:\n      base_url: https://jira.test\n      project_key: DEVEX\n      kind_mapping:\n        initiative: Task\n        feature: Story\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// documentedJira serves Atlassian's documented examples, whose project has the issue types Task
// and Bug, to the token "good", and refuses any other.
func documentedJira(t *testing.T, token string) func(config.Target) (provider.Adapter, error) {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("provider", "jira", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	client := &http.Client{Transport: roundTrip(func(request *http.Request) *http.Response {
		respond := func(status int, body string) *http.Response {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		}
		if _, password, _ := request.BasicAuth(); password != "good" {
			return respond(http.StatusUnauthorized, `{"errorMessages":["You are not authenticated."]}`)
		}
		if request.URL.Path == "/rest/api/3/myself" {
			return respond(http.StatusOK, read("myself.golden"))
		}
		return respond(http.StatusOK, read("project.golden"))
	})}
	return func(config.Target) (provider.Adapter, error) {
		return jira.NewWithClient(client, "user@example.com", token), nil
	}
}

type roundTrip func(*http.Request) *http.Response

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request), nil }

func checksNamed(checks []doctor.Check, name string) doctor.Check {
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	return doctor.Check{}
}

func TestDoctorOnlineFailsOnAnInvalidToken(t *testing.T) {
	checks := onlineChecks(context.Background(), writeJiraConfig(t), documentedJira(t, "expired"))
	if check := checksNamed(checks, "online:jira"); check.Severity != doctor.SeverityFail {
		t.Errorf("online:jira = %+v, want a failure", check)
	}
}

func TestDoctorOnlineNamesAMissingIssueType(t *testing.T) {
	checks := onlineChecks(context.Background(), writeJiraConfig(t), documentedJira(t, "good"))
	if check := checksNamed(checks, "online:jira"); check.Severity != doctor.SeverityPass {
		t.Errorf("online:jira = %+v, want a pass", check)
	}
	check := checksNamed(checks, "issue-types:jira")
	if check.Severity != doctor.SeverityFail || !strings.Contains(check.Detail, "no issue type Story") || strings.Contains(check.Detail, "Task") {
		t.Errorf("issue-types:jira = %+v, want Story named and Task passed", check)
	}
}

func TestDoctorJSONReportsReadiness(t *testing.T) {
	var out bytes.Buffer
	report := doctor.Report{Checks: []doctor.Check{{Severity: doctor.SeverityFail, Name: "online:jira", Detail: "sign in to Jira: 401"}}}
	err := printDoctorReport(ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{Format: ui.FormatJSON}), report)
	var doc struct {
		Ready  bool `json:"ready"`
		Checks []struct {
			Name string `json:"name"`
		} `json:"checks"`
	}
	if jsonErr := json.Unmarshal(out.Bytes(), &doc); jsonErr != nil || doc.Ready || len(doc.Checks) != 1 || err == nil {
		t.Errorf("doc = %+v (%v), err = %v; want not ready, one check, and a failing exit", doc, jsonErr, err)
	}
}

// openAdapter offers no access check, as a future provider might.
type openAdapter struct{ provider.Adapter }

func TestDoctorOnlineSkipsAnAdapterWithoutAnAccessCheck(t *testing.T) {
	checks := onlineChecks(context.Background(), writeJiraConfig(t), func(config.Target) (provider.Adapter, error) { return openAdapter{}, nil })
	if len(checks) != 0 {
		t.Errorf("checks = %+v, want none rather than a blank failure", checks)
	}
}

func TestOnlyThisRunsFailureReplacesTheError(t *testing.T) {
	// A preflight that stops apply before the loop reports nothing, whatever the receipt holds.
	if failedThisRun(nil) {
		t.Error("failedThisRun(no outcomes) = true, want the real error kept")
	}
	if !failedThisRun([]publish.Progress{{Result: provider.OperationResult{Status: "created"}}, {Result: provider.OperationResult{Status: "failed"}}}) {
		t.Error("failedThisRun(a failure) = false")
	}
}
