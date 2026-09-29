package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	gh "github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

func githubOperation(item domain.ItemID, title string, body string) provider.Operation {
	idempotencyMarker := marker("audit", item)
	return provider.Operation{
		ID:             "create-" + string(item),
		Action:         provider.ActionCreateIssue,
		ItemID:         item,
		IdempotencyKey: idempotencyMarker,
		Fields: map[string]any{
			"title":  title,
			"body":   body + "\n\n" + idempotencyMarker,
			"labels": []string{"audit"},
		},
	}
}

// renderedIssue renders an issue as devex would have written it for operation.
func renderedIssue(number int, operation provider.Operation) map[string]any {
	title := operation.Fields["title"].(string)
	return map[string]any{
		"id":       number * 10,
		"number":   number,
		"title":    title,
		"html_url": "https://github.test/issues/" + string(rune('0'+number)),
		"body":     stampedBody(operation, title, operation.Fields["body"].(string)),
	}
}

func listClient(t *testing.T, issues ...map[string]any) *gh.Client {
	encoded, _ := json.Marshal(issues)
	return newTestClient(t, func(_ *http.Request) *http.Response {
		return jsonResponse(string(encoded))
	})
}

func TestSyncPlansReusesUpdatesAndCreates(t *testing.T) {
	unchanged := githubOperation("INIT-001", "Audit logs", "Deliver audit logs.")
	changed := githubOperation("WI-001", "Store events", "Store events durably.")
	edited := githubOperation("WI-002", "Search events", "Search stored events.")
	missing := githubOperation("WI-003", "Export events", "Export events.")
	orphan := githubOperation("WI-099", "Dropped", "Dropped.")

	editedIssue := renderedIssue(3, githubOperation("WI-002", "Search events", "Search events."))
	editedIssue["title"] = "Search events, edited by hand"
	client := listClient(t,
		renderedIssue(1, unchanged),
		renderedIssue(2, githubOperation("WI-001", "Store events", "Store events.")),
		editedIssue,
		renderedIssue(9, orphan),
	)

	synced, warnings := syncAudit(t, client, []provider.Operation{unchanged, changed, edited, missing}, false)

	actions := make([]string, 0, len(synced))
	for _, operation := range synced {
		actions = append(actions, operation.ID)
	}
	if got := strings.Join(actions, " "); got != "reuse-INIT-001 update-WI-001 reuse-WI-002 create-WI-003" {
		t.Fatalf("operations = %s", got)
	}
	if changedFields := synced[1].Fields["changed"].([]string); strings.Join(changedFields, ",") != "body" {
		t.Fatalf("changed = %v, want [body]", changedFields)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "GitHub issue #3 (WI-002) changed since devex last wrote it") || !strings.Contains(joined, "GitHub issue #9 (") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestSyncWithForceUpdatesEditedIssues(t *testing.T) {
	edited := githubOperation("WI-002", "Search events", "Search stored events.")
	issue := renderedIssue(3, githubOperation("WI-002", "Search events", "Search events."))
	issue["body"] = issue["body"].(string) + "\nA note added on GitHub."

	synced, _ := syncAudit(t, listClient(t, issue), []provider.Operation{edited}, true)

	if synced[0].Action != provider.ActionUpdateIssue || synced[0].Fields["expected_live"] == "" {
		t.Fatalf("operation = %#v", synced[0])
	}
}

func TestExecuteUpdateEditsTheIssueAndKeepsHumanLabels(t *testing.T) {
	previous := githubOperation("WI-001", "Store events", "Store events.")
	desired := githubOperation("WI-001", "Store events", "Store events durably.")
	current := renderedIssue(2, previous)
	current["labels"] = []map[string]string{{"name": "in-progress"}}
	synced, _ := syncAudit(t, listClient(t, current), []provider.Operation{desired}, false)

	var edit gh.IssueRequest
	client := newTestClient(t, func(request *http.Request) *http.Response {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/repos/project-init/devex/issues/2":
			encoded, _ := json.Marshal(current)
			return jsonResponse(string(encoded))
		case request.Method == http.MethodPatch && request.URL.Path == "/repos/project-init/devex/issues/2":
			if err := json.NewDecoder(request.Body).Decode(&edit); err != nil {
				t.Fatal(err)
			}
			return jsonResponse(`{"id":20,"number":2,"html_url":"https://github.test/issues/2"}`)
		}
		t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		return nil
	})

	remote, err := NewWithClient(client).Execute(context.Background(), githubTarget(), synced[0], publishedAt("WI-001", 2))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(edit.GetBody(), "Store events durably.") || readStamp(edit.GetBody()).Source["body"] != provider.Digest(desired.Fields["body"]) {
		t.Fatalf("body = %q", edit.GetBody())
	}
	if labels := strings.Join(*edit.Labels, ","); labels != "audit,in-progress" {
		t.Fatalf("labels = %s, want the planned label plus the human one", labels)
	}
	if remote.Key != "2" {
		t.Fatalf("remote = %#v", remote)
	}
}

func TestExecuteUpdateRefusesAnIssueEditedAfterThePlan(t *testing.T) {
	previous := githubOperation("WI-001", "Store events", "Store events.")
	current := renderedIssue(2, previous)
	synced, _ := syncAudit(t, listClient(t, current), []provider.Operation{githubOperation("WI-001", "Store events", "Store events durably.")}, false)
	current["title"] = "Store events, edited after the plan"

	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodGet {
			t.Fatalf("unexpected write %s %s", request.Method, request.URL.Path)
		}
		encoded, _ := json.Marshal(current)
		return jsonResponse(string(encoded))
	})

	_, err := NewWithClient(client).Execute(context.Background(), githubTarget(), synced[0], publishedAt("WI-001", 2))
	if err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

// The stamp must stay out of the live digest, or every write would read as a human edit.
func TestLiveDigestIgnoresDevexComments(t *testing.T) {
	operation := githubOperation("WI-001", "Store events", "Store events.")
	body := operation.Fields["body"].(string)

	if liveDigest("Store events", stampedBody(operation, "Store events", body)) != liveDigest("Store events", body) {
		t.Fatal("the stamp changed the live digest")
	}
}

func TestSyncSkipsAnIssueWhoseStampWasDamaged(t *testing.T) {
	previous := githubOperation("WI-001", "Store events", "Store events.")
	issue := renderedIssue(2, previous)
	issue["body"] = strings.Replace(issue["body"].(string), `{"id"`, `{"id`, 1)

	synced, warnings := syncAudit(t, listClient(t, issue), []provider.Operation{githubOperation("WI-001", "Store events", "Store events durably.")}, false)

	if synced[0].Action != provider.ActionReuseIssue || len(warnings) != 1 {
		t.Fatalf("operation = %s, warnings = %v", synced[0].ID, warnings)
	}
}

// publishedAt is what apply resolves for an item already published as issue number.
func publishedAt(item domain.ItemID, number int) map[domain.ItemID]provider.RemoteRef {
	return map[domain.ItemID]provider.RemoteRef{item: {Key: strconv.Itoa(number)}}
}

func syncAudit(t *testing.T, client *gh.Client, operations []provider.Operation, force bool) ([]provider.Operation, []string) {
	t.Helper()
	synced, warnings, err := NewWithClient(client).Sync(context.Background(), githubTarget(), provider.SyncRequest{
		DiscoveryID: "audit",
		Operations:  operations,
		Force:       force,
	})
	if err != nil {
		t.Fatal(err)
	}

	return synced, warnings
}

// A retry of an update that landed before apply recorded it must succeed, not blame an edit.
func TestExecuteUpdateAcceptsItsOwnLandedWrite(t *testing.T) {
	desired := githubOperation("WI-001", "Store events", "Store events durably.")
	synced, _ := syncAudit(t, listClient(t, renderedIssue(2, githubOperation("WI-001", "Store events", "Store events."))), []provider.Operation{desired}, false)
	landed := renderedIssue(2, desired)

	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodGet {
			t.Fatalf("unexpected write %s %s", request.Method, request.URL.Path)
		}
		encoded, _ := json.Marshal(landed)
		return jsonResponse(string(encoded))
	})

	remote, err := NewWithClient(client).Execute(context.Background(), githubTarget(), synced[0], publishedAt("WI-001", 2))
	if err != nil || remote.Key != "2" {
		t.Fatalf("remote = %#v, err = %v", remote, err)
	}
}

// A body pasted into a new issue carries the marker too; the original, older issue must win.
func TestSyncMatchesTheOldestIssueForAMarker(t *testing.T) {
	operation := githubOperation("WI-001", "Store events", "Store events.")
	copied := renderedIssue(8, operation)
	copied["title"] = "A copy someone filed"

	synced, _ := syncAudit(t, listClient(t, copied, renderedIssue(2, operation)), []provider.Operation{operation}, false)

	if synced[0].Action != provider.ActionReuseIssue || !strings.Contains(synced[0].Summary, "#2") {
		t.Fatalf("operation = %#v, want issue #2 kept", synced[0])
	}
}

// Updates keep every label on the issue, so dropping one from the bundle has nothing to write.
func TestSyncKeepsAnIssueWhoseOnlyLabelChangeIsARemoval(t *testing.T) {
	previous := githubOperation("WI-001", "Store events", "Store events.")
	previous.Fields["labels"] = []string{"audit", "backend"}
	issue := renderedIssue(2, previous)
	issue["labels"] = []map[string]string{{"name": "audit"}, {"name": "backend"}}

	synced, _ := syncAudit(t, listClient(t, issue), []provider.Operation{githubOperation("WI-001", "Store events", "Store events.")}, false)

	if synced[0].Action != provider.ActionReuseIssue {
		t.Fatalf("operation = %#v, want a reuse", synced[0])
	}
}
