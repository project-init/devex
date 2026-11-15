package jira

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// onlineJira serves Atlassian's documented examples for the current user and a project, whose
// issue types are Task and Bug, to the token "good", and refuses any other token.
func onlineJira(t *testing.T, token string) *Adapter {
	t.Helper()
	adapter, _ := countingJira(t, token)
	return adapter
}

// countingJira is onlineJira, also counting the project reads.
func countingJira(t *testing.T, token string) (*Adapter, *int) {
	t.Helper()
	projectReads := 0
	fixture := func(name string) string {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if _, password, _ := request.BasicAuth(); password != "good" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{"errorMessages":["You are not authenticated."]}`))}
		}
		switch request.URL.Path {
		case "/rest/api/3/myself":
			return jiraJSONResponse(fixture("myself.golden"))
		case "/rest/api/3/project/DEVEX":
			projectReads++
			return jiraJSONResponse(fixture("project.golden"))
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"errorMessages":["No project could be found with key 'NOPE'."]}`))}
		}
	})}
	return NewWithClient(client, "user@example.com", token), &projectReads
}

func TestCheckAccessSignsInAndReadsTheProject(t *testing.T) {
	detail, err := onlineJira(t, "good").CheckAccess(context.Background(), jiraTarget("https://jira.test"))
	if err != nil || detail != "signed in as Mia Krystof; project EX is reachable" {
		t.Errorf("CheckAccess() = %q, %v", detail, err)
	}
}

func TestCheckAccessFailsOnABadToken(t *testing.T) {
	if _, err := onlineJira(t, "expired").CheckAccess(context.Background(), jiraTarget("https://jira.test")); err == nil || !strings.Contains(err.Error(), "sign in to Jira") {
		t.Errorf("CheckAccess() error = %v, want the sign-in failure", err)
	}
}

func TestCheckAccessNamesAMissingProject(t *testing.T) {
	target := jiraTarget("https://jira.test")
	target.Jira.ProjectKey = "NOPE"
	if _, err := onlineJira(t, "good").CheckAccess(context.Background(), target); err == nil || !strings.Contains(err.Error(), "the Jira project NOPE does not exist") {
		t.Errorf("CheckAccess() error = %v, want the missing project named", err)
	}
}

func TestMissingIssueTypesNamesWhatTheProjectLacks(t *testing.T) {
	target := jiraTarget("https://jira.test")
	target.Jira.KindMapping = map[domain.ItemKind]string{
		domain.KindInitiative: "Epic", domain.KindFeature: "Story", domain.KindDefect: "Bug", domain.KindTask: "task",
	}
	adapter, projectReads := countingJira(t, "good")
	if _, err := adapter.CheckAccess(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	missing, err := adapter.MissingIssueTypes(context.Background(), target)
	if err != nil || strings.Join(missing, ",") != "Epic,Story" {
		t.Errorf("MissingIssueTypes() = %q, %v; want Epic and Story, matched without case", missing, err)
	}
	if *projectReads != 1 {
		t.Errorf("project reads = %d, want 1 shared by both checks", *projectReads)
	}
	target.Jira.KindMapping = nil
	fresh, freshReads := countingJira(t, "good")
	if missing, err := fresh.MissingIssueTypes(context.Background(), target); err != nil || missing != nil || *freshReads != 0 {
		t.Errorf("MissingIssueTypes(no kind_mapping) = %q, %v after %d reads; want nothing to check and no read", missing, err, *freshReads)
	}
}

func TestPreflightRejectsAPlanThatCreatesAMissingType(t *testing.T) {
	operations := []provider.Operation{
		{Action: provider.ActionCreateIssue, Fields: map[string]any{"issue_type": "Task"}},
		{Action: provider.ActionCreateIssue, Fields: map[string]any{"issue_type": "Story"}},
		{Action: provider.ActionUpdateIssue, Fields: map[string]any{"issue_type": "Epic"}}, // updates create nothing
	}
	err := onlineJira(t, "good").Preflight(context.Background(), jiraTarget("https://jira.test"), operations)
	if err == nil || !strings.Contains(err.Error(), "has no issue type Story;") {
		t.Errorf("Preflight() = %v, want Story named and Epic left out", err)
	}
	if err := onlineJira(t, "good").Preflight(context.Background(), jiraTarget("https://jira.test"), operations[:1]); err != nil {
		t.Errorf("Preflight(Task only) = %v, want nil", err)
	}
}
