package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// fakeJira serves one discovery's published issues: their stamps, live fields, and links.
type fakeJira struct {
	t            *testing.T
	stamps       map[string]provider.Stamp
	summaries    map[string]string
	types        map[string]string
	labels       map[string]string
	stampJSON    map[string]string
	links        map[string]string
	puts         map[string]string
	deletedIDs   []string
	issueReads   int
	failStamp    bool
	keepOnDelete bool
}

func (f *fakeJira) issueJSON(key string) string {
	return `{"id":"1` + strings.TrimPrefix(key, "DEVEX-") + `","key":"` + key + `","fields":{"summary":"` + f.summaries[key] +
		`","description":{"type":"doc"},"issuetype":{"name":"` + f.types[key] + `"},"labels":` + f.labelsJSON(key) +
		`,"issuelinks":[` + f.links[key] + `]}}`
}

// withStamp renders an issue with its stamp property, as a request for the property returns it.
func (f *fakeJira) withStamp(key string) string {
	encoded, _ := json.Marshal(f.stamps[key])
	if raw, exists := f.stampJSON[key]; exists {
		encoded = []byte(raw)
	}
	issue := f.issueJSON(key)

	return issue[:len(issue)-1] + `,"properties":{"` + propertyKey + `":` + string(encoded) + `}}`
}

func (f *fakeJira) labelsJSON(key string) string {
	if labels, exists := f.labels[key]; exists {
		return labels
	}

	return `["` + generatedLabel + `","audit"]`
}

func (f *fakeJira) client() *http.Client {
	return &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		path := request.URL.Path
		switch {
		case path == "/rest/api/3/search/jql":
			issues := make([]string, 0, len(f.stamps))
			for key := range f.stamps {
				issues = append(issues, f.withStamp(key))
			}
			return jiraJSONResponse(`{"issues":[` + strings.Join(issues, ",") + `],"isLast":true}`)
		case request.Method == http.MethodPut && f.failStamp && strings.Contains(path, "/properties/"):
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}
		case request.Method == http.MethodPut:
			body, _ := io.ReadAll(request.Body)
			f.puts[path] = string(body)
			return jiraJSONResponse(`{}`)
		case request.Method == http.MethodDelete:
			f.deletedIDs = append(f.deletedIDs, strings.TrimPrefix(path, "/rest/api/3/issueLink/"))
			if f.keepOnDelete {
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}
		case strings.HasPrefix(path, "/rest/api/3/issue/") && request.Method == http.MethodGet:
			f.issueReads++
			key := strings.TrimPrefix(path, "/rest/api/3/issue/")
			if request.URL.Query().Get("properties") == propertyKey {
				return jiraJSONResponse(f.withStamp(key))
			}
			return jiraJSONResponse(f.issueJSON(key))
		}
		f.t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		return nil
	})}
}

func (f *fakeJira) adapter() *Adapter {
	return NewWithClient(f.client(), "user@example.com", "token")
}

func (f *fakeJira) sync(request provider.SyncRequest) ([]provider.Operation, []string) {
	f.t.Helper()
	request.DiscoveryID = "audit"
	synced, warnings, err := f.adapter().Sync(context.Background(), jiraTarget("https://jira.test"), request)
	if err != nil {
		f.t.Fatal(err)
	}

	return synced, warnings
}

func (f *fakeJira) execute(operation provider.Operation, resolved map[domain.ItemID]provider.RemoteRef) (provider.RemoteRef, error) {
	return f.adapter().Execute(context.Background(), jiraTarget("https://jira.test"), operation, resolved)
}

func operationByID(operations []provider.Operation, id string) provider.Operation {
	for _, operation := range operations {
		if operation.ID == id {
			return operation
		}
	}

	return provider.Operation{}
}

// publishedAs is what apply resolves for an item already published as issueKey.
func publishedAs(item domain.ItemID, issueKey string) map[domain.ItemID]provider.RemoteRef {
	return map[domain.ItemID]provider.RemoteRef{item: {Key: issueKey}}
}

func issueOperation(item domain.ItemID, title string, description string) provider.Operation {
	return provider.Operation{
		ID:             "create-" + string(item),
		Action:         provider.ActionCreateIssue,
		ItemID:         item,
		IdempotencyKey: "audit/" + string(item),
		Fields: map[string]any{
			"project_key":         "DEVEX",
			"issue_type":          "Task",
			"title":               title,
			"description":         description,
			"acceptance_criteria": []string{},
			"document_url":        "",
			"labels":              []string{generatedLabel, "audit"},
			"parent_item_id":      "",
		},
	}
}

func stampFor(operation provider.Operation, summary string) provider.Stamp {
	return provider.NewStamp(operation, sourceFields, provider.Digest(summary, map[string]any{"type": "doc"}))
}

func newSyncFixture(t *testing.T) (*fakeJira, []provider.Operation) {
	unchanged := issueOperation("INIT-001", "Audit logs", "Deliver audit logs.")
	changed := issueOperation("WI-001", "Store events", "Store events durably.")
	edited := issueOperation("WI-002", "Search events", "Search stored events.")
	orphan := issueOperation("WI-099", "Dropped", "Dropped from the bundle.")
	previous := issueOperation("WI-001", "Store events", "Store events.")
	editedBefore := issueOperation("WI-002", "Search events", "Search events.")

	fake := &fakeJira{
		t: t,
		stamps: map[string]provider.Stamp{
			"DEVEX-1": stampFor(unchanged, "Audit logs"),
			"DEVEX-2": stampFor(previous, "Store events"),
			"DEVEX-3": stampFor(editedBefore, "Search events"),
			"DEVEX-9": stampFor(orphan, "Dropped"),
		},
		summaries: map[string]string{
			"DEVEX-1": "Audit logs", "DEVEX-2": "Store events", "DEVEX-3": "Search events, edited by hand", "DEVEX-9": "Dropped",
		},
		types: map[string]string{"DEVEX-1": "Task", "DEVEX-2": "Task", "DEVEX-3": "Task", "DEVEX-9": "Task"},
		links: map[string]string{
			"DEVEX-3": `{"id":"501","type":{"name":"Blocks"},"inwardIssue":{"key":"DEVEX-2"}},` +
				`{"id":"502","type":{"name":"Blocks"},"inwardIssue":{"key":"DEVEX-1"}},` +
				`{"id":"503","type":{"name":"Blocks"},"inwardIssue":{"key":"OTHER-5"}},` +
				`{"id":"504","type":{"name":"Relates"},"inwardIssue":{"key":"DEVEX-1"}}`,
		},
		labels: map[string]string{},
		puts:   map[string]string{},
	}
	operations := []provider.Operation{
		unchanged,
		changed,
		edited,
		issueOperation("WI-003", "Export events", "Export events."),
		linkOperation("audit", "WI-002", "WI-001", defaultLinkType),
	}

	return fake, operations
}

func TestSyncPlansReusesUpdatesCreatesAndUnlinks(t *testing.T) {
	fake, operations := newSyncFixture(t)

	synced, warnings := fake.sync(provider.SyncRequest{Operations: operations})

	if operationByID(synced, "reuse-INIT-001").Action != provider.ActionReuseIssue {
		t.Fatalf("unchanged issue: %#v", synced)
	}
	update := operationByID(synced, "update-WI-001")
	if changed := update.Fields["changed"].([]string); len(changed) != 1 || changed[0] != "description" {
		t.Fatalf("update changed = %v, want [description]", update.Fields["changed"])
	}
	if update.Fields["expected_live"] == "" {
		t.Fatalf("update fields = %#v", update.Fields)
	}
	if operationByID(synced, "reuse-WI-002").Action != provider.ActionReuseIssue {
		t.Fatalf("an issue edited in Jira must be kept, not updated: %#v", synced)
	}
	if operationByID(synced, "create-WI-003").Action != provider.ActionCreateIssue {
		t.Fatalf("a missing issue must stay a create: %#v", synced)
	}
	if operationByID(synced, "link-WI-001/WI-002").ID != "" {
		t.Fatal("an existing link must not be planned again")
	}
	unlink := operationByID(synced, "unlink-INIT-001/WI-002")
	if unlink.Action != provider.ActionUnlinkIssues || unlink.Fields["link_id"] != "502" || unlink.Fields["blocked_key"] != "DEVEX-3" {
		t.Fatalf("stale link: %#v", unlink)
	}
	if len(synced) != 5 {
		t.Fatalf("operations = %d, want 5: links outside the discovery and of other types stay untouched", len(synced))
	}
	if fake.issueReads != 0 {
		t.Fatalf("issue reads = %d, want the search alone", fake.issueReads)
	}

	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "Jira DEVEX-3 (WI-002) changed since devex last wrote it") ||
		!strings.Contains(joined, "Jira DEVEX-9 (audit/WI-099) is no longer in the bundle") {
		t.Fatalf("warnings = %v", warnings)
	}
}

// The plan warned about the links it planned; the sync plan must describe only the links it
// still changes.
func TestSyncReplacesThePlannedLinkWarning(t *testing.T) {
	fake, operations := newSyncFixture(t)
	fake.links["DEVEX-1"] = `{"id":"601","type":{"name":"Relates"},"inwardIssue":{"key":"TRACK-1"}}`
	operations = append(operations, trackingOperation("audit", "INIT-001", "TRACK-1"))
	planned := linkPermissionWarning(operations)

	_, warnings := fake.sync(provider.SyncRequest{Operations: operations, Warnings: []string{"kept", planned}})

	joined := strings.Join(warnings, "\n")
	if strings.Contains(joined, planned) || !strings.Contains(joined, "these link types: Blocks.") || warnings[0] != "kept" {
		t.Fatalf("warnings = %v, want the plan's other warnings and a link warning for the unlink alone", warnings)
	}
}

func TestSyncWithForceUpdatesEditedIssues(t *testing.T) {
	fake, operations := newSyncFixture(t)

	synced, _ := fake.sync(provider.SyncRequest{Operations: operations, Force: true})

	// Forcing over an edit rewrites both live fields, so the edit cannot survive into the digest.
	if forced := operationByID(synced, "update-WI-002"); strings.Join(forced.Fields["changed"].([]string), ",") != "description,summary" {
		t.Fatalf("forced edited issue = %#v", synced)
	}
}

func TestSyncRewritesIssuesStampedBeforeSync(t *testing.T) {
	fake, operations := newSyncFixture(t)
	fake.stamps["DEVEX-1"] = provider.Stamp{ID: "audit/INIT-001"}

	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})

	if synced[0].Action != provider.ActionUpdateIssue {
		t.Fatalf("an unstamped issue = %#v, want an update that records a stamp", synced[0])
	}
	// The issue already carries every planned label, so only the fields a rewrite changes remain.
	if changed := synced[0].Fields["changed"].([]string); strings.Join(changed, ",") != "description,summary" {
		t.Fatalf("changed = %v, want the summary and description", changed)
	}
}

func TestSyncKeepsAnAppliedTrackingLink(t *testing.T) {
	fake, operations := newSyncFixture(t)
	fake.links["DEVEX-1"] = `{"id":"601","type":{"name":"Relates"},"inwardIssue":{"key":"TRACK-1"}}`
	operations = append(operations, trackingOperation("audit", "INIT-001", "TRACK-1"))

	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})

	for _, operation := range synced {
		if operation.Action == provider.ActionLinkIssues {
			t.Fatalf("an existing tracking link must not be planned again: %s", operation.ID)
		}
	}
}

func TestSyncWarnsThatAParentCannotBeRemoved(t *testing.T) {
	fake, operations := newSyncFixture(t)
	withParent := issueOperation("INIT-001", "Audit logs", "Deliver audit logs.")
	withParent.Fields["parent_item_id"] = "INIT-000"
	fake.stamps["DEVEX-1"] = stampFor(withParent, "Audit logs")

	synced, warnings := fake.sync(provider.SyncRequest{Operations: operations})

	if synced[0].Action != provider.ActionReuseIssue || !strings.Contains(strings.Join(warnings, "\n"), "devex leaves its Jira parent in place") {
		t.Fatalf("operation = %s, warnings = %v", synced[0].ID, warnings)
	}
}

func TestExecuteUpdateRewritesChangedFieldsAndStamps(t *testing.T) {
	fake, operations := newSyncFixture(t)
	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})

	remote, err := fake.execute(operationByID(synced, "update-WI-001"), publishedAs("WI-001", "DEVEX-2"))
	if err != nil {
		t.Fatal(err)
	}

	var edit struct {
		Fields     map[string]any `json:"fields"`
		Properties []struct {
			Key   string         `json:"key"`
			Value provider.Stamp `json:"value"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(fake.puts["/rest/api/3/issue/DEVEX-2"]), &edit); err != nil {
		t.Fatal(err)
	}
	if _, rewritten := edit.Fields["summary"]; rewritten || edit.Fields["description"] == nil {
		t.Fatalf("fields = %v, want only the description", edit.Fields)
	}
	if len(edit.Properties) != 1 || edit.Properties[0].Value.Source["description"] != provider.Digest("Store events durably.") {
		t.Fatalf("properties = %+v, want the stamp in the same request", edit.Properties)
	}
	var stamp provider.Stamp
	if err := json.Unmarshal([]byte(fake.puts["/rest/api/3/issue/DEVEX-2/properties/"+propertyKey]), &stamp); err != nil {
		t.Fatal(err)
	}
	if stamp.ID != "audit/WI-001" || stamp.Live == "" {
		t.Fatalf("stamp = %#v, want the live digest recorded after the write", stamp)
	}
	if remote.Key != "DEVEX-2" {
		t.Fatalf("remote = %#v", remote)
	}
}

// Recording the live digest is a second request. Its failure must not fail an update that
// already landed with its stamp.
func TestExecuteUpdateSurvivesAFailedLiveRecord(t *testing.T) {
	fake, operations := newSyncFixture(t)
	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})
	fake.failStamp = true

	if _, err := fake.execute(operationByID(synced, "update-WI-001"), publishedAs("WI-001", "DEVEX-2")); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(fake.puts["/rest/api/3/issue/DEVEX-2"], `"properties"`) {
		t.Fatalf("edit = %s, want the stamp", fake.puts["/rest/api/3/issue/DEVEX-2"])
	}
}

func TestExecuteUpdateRefusesAnIssueEditedAfterThePlan(t *testing.T) {
	fake, operations := newSyncFixture(t)
	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})
	fake.summaries["DEVEX-2"] = "Store events, edited after the plan"

	_, err := fake.execute(operationByID(synced, "update-WI-001"), publishedAs("WI-001", "DEVEX-2"))

	if err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if len(fake.puts) != 0 {
		t.Fatalf("puts = %v, want none", fake.puts)
	}
}

func unlinkOperation() provider.Operation {
	return provider.Operation{
		ID:     "unlink-INIT-001/WI-002",
		Action: provider.ActionUnlinkIssues,
		Fields: map[string]any{"link_id": "502", "link_type": "Blocks", "blocking_key": "DEVEX-1", "blocked_key": "DEVEX-3"},
	}
}

func TestExecuteUnlinkDeletesTheLink(t *testing.T) {
	fake, _ := newSyncFixture(t)

	remote, err := fake.execute(unlinkOperation(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(fake.deletedIDs) != 1 || fake.deletedIDs[0] != "502" || remote.Key != "DEVEX-3" {
		t.Fatalf("deleted = %v, remote = %#v", fake.deletedIDs, remote)
	}
}

// Jira answers 404 both for a link already gone and for one the caller cannot see.
func TestExecuteUnlinkFailsWhenA404LeavesTheLink(t *testing.T) {
	fake, _ := newSyncFixture(t)
	fake.keepOnDelete = true

	if _, err := fake.execute(unlinkOperation(), nil); err == nil {
		t.Fatal("want an error while the link remains")
	}

	delete(fake.links, "DEVEX-3")
	if _, err := fake.execute(unlinkOperation(), nil); err != nil {
		t.Fatalf("a link already gone = %v, want success", err)
	}
}

// A retry of an update that landed before apply recorded it must succeed, not blame an edit.
func TestExecuteUpdateAcceptsItsOwnLandedWrite(t *testing.T) {
	fake, operations := newSyncFixture(t)
	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})
	fake.summaries["DEVEX-2"] = "Store events, as Jira stored devex's write"
	fake.stamps["DEVEX-2"] = provider.NewStamp(operations[1], sourceFields, "")

	if _, err := fake.execute(operationByID(synced, "update-WI-001"), publishedAs("WI-001", "DEVEX-2")); err != nil {
		t.Fatal(err)
	}

	if _, rewritten := fake.puts["/rest/api/3/issue/DEVEX-2"]; rewritten {
		t.Fatal("a landed update must not be sent again")
	}
}

// Jira updates only add labels, so dropping one from the bundle has nothing to write.
func TestSyncKeepsAnIssueWhoseOnlyLabelChangeIsARemoval(t *testing.T) {
	fake, operations := newSyncFixture(t)
	withExtra := issueOperation("INIT-001", "Audit logs", "Deliver audit logs.")
	withExtra.Fields["labels"] = []string{generatedLabel, "audit", "backend"}
	fake.stamps["DEVEX-1"] = stampFor(withExtra, "Audit logs")
	fake.labels["DEVEX-1"] = `["` + generatedLabel + `","audit","backend"]`

	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})

	if synced[0].Action != provider.ActionReuseIssue {
		t.Fatalf("operation = %#v, want a reuse", synced[0])
	}
}

// A label change cannot overwrite a human edit, so it proceeds and keeps the recorded live digest.
func TestSyncAddsLabelsToAnEditedIssueAndKeepsItsLiveDigest(t *testing.T) {
	fake, operations := newSyncFixture(t)
	labelled := issueOperation("WI-002", "Search events", "Search events.")
	labelled.Fields["labels"] = []string{generatedLabel, "audit", "backend"}
	operations[2] = labelled

	synced, warnings := fake.sync(provider.SyncRequest{Operations: operations})

	update := operationByID(synced, "update-WI-002")
	if changed := update.Fields["changed"].([]string); strings.Join(changed, ",") != "labels" {
		t.Fatalf("changed = %v, want [labels]", changed)
	}
	if strings.Contains(strings.Join(warnings, "\n"), "WI-002") {
		t.Fatalf("warnings = %v, want none for a label change", warnings)
	}
	if _, err := fake.execute(update, publishedAs("WI-002", "DEVEX-3")); err != nil {
		t.Fatal(err)
	}
	if _, recorded := fake.puts["/rest/api/3/issue/DEVEX-3/properties/"+propertyKey]; recorded {
		t.Fatal("a label change must not record the human-edited content as devex's")
	}
	if !strings.Contains(fake.puts["/rest/api/3/issue/DEVEX-3"], fake.stamps["DEVEX-3"].Live) {
		t.Fatalf("edit = %s, want the stamp to keep the recorded live digest", fake.puts["/rest/api/3/issue/DEVEX-3"])
	}
}

func TestSyncTreatsAMalformedStampAsEdited(t *testing.T) {
	fake, operations := newSyncFixture(t)
	fake.stampJSON = map[string]string{"DEVEX-2": `{"id":"audit/WI-001","source":["damaged"]}`}

	synced, warnings := fake.sync(provider.SyncRequest{Operations: operations})

	if operationByID(synced, "reuse-WI-001").ID == "" || !strings.Contains(strings.Join(warnings, "\n"), "Jira DEVEX-2 (WI-001) may have changed") {
		t.Fatalf("operations = %v, warnings = %v", synced, warnings)
	}
}

// A lost create response and its retry can mark two issues; the original, oldest one must win.
func TestSyncMatchesTheOldestIssueForAMarker(t *testing.T) {
	fake, operations := newSyncFixture(t)
	fake.stamps["DEVEX-12"] = fake.stamps["DEVEX-2"]
	fake.summaries["DEVEX-12"] = fake.summaries["DEVEX-2"]
	fake.types["DEVEX-12"] = "Task"

	synced, _ := fake.sync(provider.SyncRequest{Operations: operations})

	if summary := operationByID(synced, "update-WI-001").Summary; !strings.Contains(summary, "Jira DEVEX-2 ") {
		t.Fatalf("summary = %q, want the older DEVEX-2", summary)
	}
}

func TestSyncTreatsAStampWithoutSourceDigestsAsEdited(t *testing.T) {
	fake, operations := newSyncFixture(t)
	fake.stampJSON = map[string]string{"DEVEX-2": `{"id":"audit/WI-001","live":"recorded"}`}

	synced, warnings := fake.sync(provider.SyncRequest{Operations: operations})

	if operationByID(synced, "reuse-WI-001").ID == "" || !strings.Contains(strings.Join(warnings, "\n"), "Jira DEVEX-2 (WI-001) may have changed") {
		t.Fatalf("operations = %v, warnings = %v", synced, warnings)
	}
}

// A skipped description change must survive the partial update, so the next sync still offers it.
func TestSyncKeepsSkippedChangesPendingAfterAPartialUpdate(t *testing.T) {
	fake, operations := newSyncFixture(t)
	operations[2].Fields["labels"] = []string{generatedLabel, "audit", "backend"}

	synced, warnings := fake.sync(provider.SyncRequest{Operations: operations})
	update := operationByID(synced, "update-WI-002")
	if strings.Join(update.Fields["changed"].([]string), ",") != "labels" || !strings.Contains(strings.Join(warnings, "\n"), "skipped description") {
		t.Fatalf("update = %#v, warnings = %v", update, warnings)
	}
	if _, err := fake.execute(update, publishedAs("WI-002", "DEVEX-3")); err != nil {
		t.Fatal(err)
	}

	var edit struct {
		Properties []struct {
			Value provider.Stamp `json:"value"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(fake.puts["/rest/api/3/issue/DEVEX-3"]), &edit); err != nil {
		t.Fatal(err)
	}
	fake.stamps["DEVEX-3"] = edit.Properties[0].Value
	fake.labels["DEVEX-3"] = `["` + generatedLabel + `","audit","backend"]`

	_, warnings = fake.sync(provider.SyncRequest{Operations: operations})
	if !strings.Contains(strings.Join(warnings, "\n"), "Jira DEVEX-3 (WI-002) changed since devex last wrote it; skipped description") {
		t.Fatalf("warnings = %v, want the description change still pending", warnings)
	}
}
