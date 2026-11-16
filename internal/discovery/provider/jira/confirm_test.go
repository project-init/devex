package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/provider"
)

// documentedIssue is Jira's documented example issue, moved into project and stamped with id.
func documentedIssue(t *testing.T, project, id string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/issue.golden")
	if err != nil {
		t.Fatal(err)
	}
	var issue map[string]any
	if err := json.Unmarshal(data, &issue); err != nil {
		t.Fatal(err)
	}
	issue["fields"].(map[string]any)["project"].(map[string]any)["key"] = project
	issue["properties"] = map[string]any{propertyKey: map[string]any{"id": id}}
	issue["fields"].(map[string]any)["labels"] = []string{generatedLabel, "audit"}
	data, _ = json.Marshal(issue)
	return string(data)
}

func confirmJira(t *testing.T, respond func(path string) *http.Response) *Adapter {
	t.Helper()
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if request.Method == http.MethodGet && request.URL.Query().Get("properties") != propertyKey {
			t.Errorf("request = %s %s, want a read of the stamp property", request.Method, request.URL)
		}
		return respond(request.URL.Path)
	})}
	return NewWithClient(client, "user@example.com", "token")
}

func TestConfirmReadsEachIssueByKey(t *testing.T) {
	adapter := confirmJira(t, func(path string) *http.Response {
		switch path {
		case "/rest/api/3/issue/ED-1":
			return jiraJSONResponse(documentedIssue(t, "DEVEX", "audit/WI-001"))
		case "/rest/api/3/issue/ED-2": // reused for another item since
			return jiraJSONResponse(documentedIssue(t, "DEVEX", "audit/WI-009"))
		case "/rest/api/3/issue/ED-3": // moved to another project
			return jiraJSONResponse(documentedIssue(t, "OTHER", "audit/WI-003"))
		case "/rest/api/3/issue/ED-5": // Jira reports the key in its own case
			return jiraJSONResponse(documentedIssue(t, "devex", "audit/WI-005"))
		default: // deleted
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`))}
		}
	})
	confirmed, err := adapter.Confirm(context.Background(), jiraTarget("https://jira.test"), map[string]provider.RemoteRef{
		"audit/WI-001": {Key: "ED-1"}, "audit/WI-002": {Key: "ED-2"}, "audit/WI-003": {Key: "ED-3"}, "audit/WI-004": {Key: "ED-4"},
		"audit/WI-005": {Key: "ED-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed) != 2 || confirmed["audit/WI-001"].Key != "ED-1" || confirmed["audit/WI-001"].ID != "10002" {
		t.Errorf("Confirm() = %#v, want ED-1 and ED-5, still stamped for their items in DEVEX", confirmed)
	}
	if _, ok := confirmed["audit/WI-005"]; !ok {
		t.Errorf("Confirm() lacks WI-005, whose project key differs only in case")
	}
}

func TestConfirmFailsWhenJiraCannotAnswer(t *testing.T) {
	// A 403 here means the token or account was refused, not that the issue is gone, and it is
	// not retried, so the test runs at once.
	adapter := confirmJira(t, func(string) *http.Response {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"errorMessages":["You do not have the permission to see the specified issue."],"errors":{}}`))}
	})
	if _, err := adapter.Confirm(context.Background(), jiraTarget("https://jira.test"), map[string]provider.RemoteRef{"audit/WI-001": {Key: "ED-1"}}); err == nil {
		t.Error("Confirm() = nil error, want the failed read reported rather than the issue treated as gone")
	}
}

func TestConfirmCachesTheLinksItRead(t *testing.T) {
	adapter := confirmJira(t, func(string) *http.Response {
		var issue map[string]any
		_ = json.Unmarshal([]byte(documentedIssue(t, "DEVEX", "audit/WI-001")), &issue)
		issue["fields"].(map[string]any)["issuelinks"] = []any{map[string]any{
			"id": "10001", "type": map[string]any{"name": "Blocks"}, "inwardIssue": map[string]any{"key": "ED-9"},
		}}
		data, _ := json.Marshal(issue)
		return jiraJSONResponse(string(data))
	})
	if _, err := adapter.Confirm(context.Background(), jiraTarget("https://jira.test"), map[string]provider.RemoteRef{"audit/WI-001": {Key: "ED-1"}}); err != nil {
		t.Fatal(err)
	}
	if found, cached := adapter.links.lookup("ED-1", "Blocks", "ED-9"); !found || !cached {
		t.Errorf("link cache for ED-1 = found %t, cached %t; want the read's link cached", found, cached)
	}
}

func TestConfirmReportsAProjectItCannotRead(t *testing.T) {
	adapter := confirmJira(t, func(string) *http.Response {
		var issue map[string]any
		_ = json.Unmarshal([]byte(documentedIssue(t, "DEVEX", "audit/WI-001")), &issue)
		issue["fields"].(map[string]any)["project"] = "DEVEX" // not the documented object
		data, _ := json.Marshal(issue)
		return jiraJSONResponse(string(data))
	})
	if _, err := adapter.Confirm(context.Background(), jiraTarget("https://jira.test"), map[string]provider.RemoteRef{"audit/WI-001": {Key: "ED-1"}}); err == nil {
		t.Error("Confirm() = nil error, want the unreadable project reported rather than the issue skipped")
	}
}

func TestConfirmRestoresTheLabelsTheSearchFiltersOn(t *testing.T) {
	var updates []string
	adapter := confirmJira(t, func(path string) *http.Response {
		if strings.HasSuffix(path, "ED-2") || strings.HasSuffix(path, "ED-3") {
			var issue map[string]any
			_ = json.Unmarshal([]byte(documentedIssue(t, "DEVEX", "audit/WI-00"+path[len(path)-1:])), &issue)
			issue["fields"].(map[string]any)["labels"] = []string{} // someone removed them
			issue["key"] = path[strings.LastIndex(path, "/")+1:]
			data, _ := json.Marshal(issue)
			return jiraJSONResponse(string(data))
		}
		return jiraJSONResponse(documentedIssue(t, "DEVEX", "audit/WI-001"))
	})
	adapter.httpClient.Transport = updateRecorder{adapter.httpClient.Transport, &updates, false}
	candidates := map[string]provider.RemoteRef{"audit/WI-001": {Key: "ED-1"}, "audit/WI-002": {Key: "ED-2"}, "audit/WI-003": {Key: "ED-3"}}
	if _, err := adapter.Confirm(context.Background(), jiraTarget("https://jira.test"), candidates); err != nil {
		t.Fatal(err)
	}
	if strings.Join(updates, " ") != "ED-2 ED-3" {
		t.Errorf("label updates = %q, want ED-2 and ED-3, which lost their labels", updates)
	}
}

func TestConfirmStopsRestoringLabelsAfterTheFirstRefusal(t *testing.T) {
	var updates []string
	adapter := confirmJira(t, func(path string) *http.Response {
		var issue map[string]any
		_ = json.Unmarshal([]byte(documentedIssue(t, "DEVEX", "audit/WI-00"+path[len(path)-1:])), &issue)
		issue["fields"].(map[string]any)["labels"] = []string{}
		issue["key"] = path[strings.LastIndex(path, "/")+1:]
		data, _ := json.Marshal(issue)
		return jiraJSONResponse(string(data))
	})
	adapter.httpClient.Transport = updateRecorder{adapter.httpClient.Transport, &updates, true}
	candidates := map[string]provider.RemoteRef{"audit/WI-001": {Key: "ED-1"}, "audit/WI-002": {Key: "ED-2"}, "audit/WI-003": {Key: "ED-3"}}
	confirmed, err := adapter.Confirm(context.Background(), jiraTarget("https://jira.test"), candidates)
	if err != nil || len(confirmed) != 3 || len(updates) != 1 {
		t.Errorf("Confirm() = %d confirmed, %v, %d label updates; want all three and one refused update", len(confirmed), err, len(updates))
	}
}

// updateRecorder records each issue PUT and answers it, refusing it when refuse is set; other
// requests go to next.
type updateRecorder struct {
	next    http.RoundTripper
	updates *[]string
	refuse  bool
}

func (u updateRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPut {
		return u.next.RoundTrip(request)
	}
	*u.updates = append(*u.updates, strings.TrimPrefix(request.URL.Path, "/rest/api/3/issue/"))
	if u.refuse {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"errorMessages":["You do not have permission to edit issues in this project."],"errors":{}}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
}
