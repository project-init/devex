package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

func TestExecuteCreatesJiraIssue(t *testing.T) {
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if request.Method != http.MethodPost || request.URL.Path != "/rest/api/3/issue" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		fields := body["fields"].(map[string]any)
		if fields["summary"] != "Implement audit logs" {
			t.Fatalf("fields = %#v", fields)
		}
		properties := body["properties"].([]any)
		property := properties[0].(map[string]any)
		if property["key"] != propertyKey {
			t.Fatalf("properties = %#v", properties)
		}
		return jiraJSONResponse(`{"id":"10042","key":"DEVEX-7"}`)
	})}

	target := jiraTarget("https://jira.test")
	remote, err := NewWithClient(client, "user@example.com", "token").Execute(
		context.Background(),
		target,
		provider.Operation{
			ID: "create-WI-001",
			Fields: map[string]any{
				"project_key":        "DEVEX",
				"issue_type":         "Task",
				"title":              "Implement audit logs",
				"description":        "Implement persistence.",
				"labels":             []any{generatedLabel},
				"parent_item_id":     "",
				"idempotency_marker": "audit/WI-001",
			},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Key != "DEVEX-7" || remote.URL != "https://jira.test/browse/DEVEX-7" {
		t.Fatalf("remote = %#v", remote)
	}
}

func TestResolveFindsJiraProperties(t *testing.T) {
	searchRequests := 0
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		switch request.URL.Path {
		case "/rest/api/3/search/jql":
			searchRequests++
			if request.URL.Query().Get("nextPageToken") == "page-2" {
				return jiraJSONResponse(`{"issues":[{"id":"10043","key":"DEVEX-8"}],"isLast":true}`)
			}
			return jiraJSONResponse(`{"issues":[{"id":"10042","key":"DEVEX-7"}],"nextPageToken":"page-2"}`)
		case "/rest/api/3/issue/DEVEX-7/properties/devex.discovery":
			return jiraJSONResponse(`{"value":{"id":"audit/WI-001"}}`)
		case "/rest/api/3/issue/DEVEX-8/properties/devex.discovery":
			return jiraJSONResponse(`{"value":{"id":"audit/WI-002"}}`)
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found"))}
		}
	})}
	published, err := NewWithClient(client, "user@example.com", "token").Resolve(
		context.Background(),
		jiraTarget("https://jira.test"),
		&provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{
			{ItemID: "WI-001", IdempotencyKey: "audit/WI-001"},
			{ItemID: "WI-002", IdempotencyKey: "audit/WI-002"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if published["audit/WI-001"].Key != "DEVEX-7" || published["audit/WI-002"].Key != "DEVEX-8" {
		t.Fatalf("published = %#v", published)
	}
	// One paginated scan answers every pending operation.
	if searchRequests != 2 {
		t.Fatalf("search requests = %d, want 2 pages", searchRequests)
	}
}

// An issue belonging to another bundle must not be reported as published for this one.
func TestResolveIgnoresKeysOutsideThePendingSet(t *testing.T) {
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		switch request.URL.Path {
		case "/rest/api/3/search/jql":
			return jiraJSONResponse(`{"issues":[{"id":"10042","key":"DEVEX-7"}],"isLast":true}`)
		case "/rest/api/3/issue/DEVEX-7/properties/devex.discovery":
			return jiraJSONResponse(`{"value":{"id":"billing/WI-001"}}`)
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found"))}
		}
	})}
	published, err := NewWithClient(client, "user@example.com", "token").Resolve(
		context.Background(),
		jiraTarget("https://jira.test"),
		&provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: "audit/WI-001"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 {
		t.Fatalf("published = %#v, want empty", published)
	}
}

func TestPlanEmitsDependencyLinks(t *testing.T) {
	operations, warnings, err := NewWithClient(nil, "user@example.com", "token").Plan(
		context.Background(),
		&domain.WorkBreakdown{
			Discovery: domain.DiscoveryRef{ID: "audit", Document: "discovery.md"},
			Items: []domain.WorkItem{
				{
					ID:          "WI-001",
					Kind:        domain.KindTask,
					Title:       "First",
					Description: "First.",
					Labels:      []string{"security"},
				},
				{
					ID:          "WI-002",
					Kind:        domain.KindTask,
					Title:       "Second",
					Description: "Second.",
					DependsOn:   []domain.ItemID{"WI-001"},
				},
			},
		},
		nil,
		jiraTarget("https://jira.test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Links depend on a Jira permission and link type that planning cannot check, so the plan
	// has to say so before anything is created.
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Link Issues") {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(operations) != 3 {
		t.Fatalf("operations = %d, want 3", len(operations))
	}

	// Lookup narrows its search by both labels, so planning must always attach them: one
	// marking the tool as author, one scoping the search to this bundle.
	labels, err := fieldStringSlice(operations[0], "labels")
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 3 || labels[0] != "audit" || labels[1] != generatedLabel || labels[2] != "security" {
		t.Fatalf("labels = %#v", labels)
	}

	// Links follow every create so both issues exist before the relationship is published.
	link := operations[2]
	if link.Action != actionLinkIssues || link.ID != "link-WI-001/WI-002" {
		t.Fatalf("link = %#v", link)
	}
	if link.ItemID != "" {
		t.Fatalf("link.ItemID = %q, want empty", link.ItemID)
	}
	if link.Fields["blocking_item_id"] != "WI-001" || link.Fields["blocked_item_id"] != "WI-002" {
		t.Fatalf("link fields = %#v", link.Fields)
	}
	if link.Fields["link_type"] != defaultLinkType {
		t.Fatalf("link type = %#v", link.Fields["link_type"])
	}
	if strings.Contains(operations[1].Fields["description"].(string), "Dependencies") {
		t.Fatalf("description = %#v", operations[1].Fields["description"])
	}
}

func TestExecuteCreatesIssueLink(t *testing.T) {
	created := false
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/rest/api/3/issue/DEVEX-8":
			return jiraJSONResponse(`{"fields":{"issuelinks":[]}}`)
		case request.Method == http.MethodPost && request.URL.Path == "/rest/api/3/issueLink":
			created = true
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			// Jira reads the link as inwardIssue blocks outwardIssue.
			inward := body["inwardIssue"].(map[string]any)
			outward := body["outwardIssue"].(map[string]any)
			if inward["key"] != "DEVEX-7" || outward["key"] != "DEVEX-8" {
				t.Fatalf("body = %#v", body)
			}
			if body["type"].(map[string]any)["name"] != "Blocks" {
				t.Fatalf("type = %#v", body["type"])
			}
			return jiraJSONResponse(`{}`)
		default:
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
			return nil
		}
	})}

	_, err := NewWithClient(client, "user@example.com", "token").Execute(
		context.Background(),
		jiraTarget("https://jira.test"),
		linkOperation("audit", "WI-002", "WI-001", defaultLinkType),
		map[domain.ItemID]provider.RemoteRef{
			"WI-001": {Key: "DEVEX-7", URL: "https://jira.test/browse/DEVEX-7"},
			"WI-002": {Key: "DEVEX-8", URL: "https://jira.test/browse/DEVEX-8"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("issue link was not created")
	}
}

// Hyphens are legal in item IDs, so the operation ID must separate the pair with a character
// an ID cannot contain. Otherwise two edges collide on one receipt key and one is skipped.
func TestLinkOperationIDsSurviveHyphenatedItemIDs(t *testing.T) {
	first := linkOperation("audit", "API-GATEWAY", "AUTH", defaultLinkType)
	second := linkOperation("audit", "GATEWAY", "AUTH-API", defaultLinkType)
	if first.ID == second.ID {
		t.Fatalf("operation IDs collide: %q", first.ID)
	}
	if first.IdempotencyKey == second.IdempotencyKey {
		t.Fatalf("idempotency keys collide: %q", first.IdempotencyKey)
	}
}

func TestExecuteLinkReusesCachedIssueLinks(t *testing.T) {
	issueGets := 0
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/rest/api/3/issue/") {
			issueGets++
			return jiraJSONResponse(`{"fields":{"issuelinks":[]}}`)
		}
		return jiraJSONResponse(`{}`)
	})}
	adapter := NewWithClient(client, "user@example.com", "token")
	resolved := map[domain.ItemID]provider.RemoteRef{
		"WI-001": {Key: "DEVEX-7"},
		"WI-002": {Key: "DEVEX-8"},
		"WI-003": {Key: "DEVEX-9"},
	}

	// Both edges block the same issue, so the second must read the cache rather than refetch.
	for _, dependency := range []domain.ItemID{"WI-001", "WI-002"} {
		if _, err := adapter.Execute(
			context.Background(),
			jiraTarget("https://jira.test"),
			linkOperation("audit", "WI-003", dependency, defaultLinkType),
			resolved,
		); err != nil {
			t.Fatal(err)
		}
	}
	if issueGets != 1 {
		t.Fatalf("issue reads = %d, want 1", issueGets)
	}
}

func TestExecuteLinkReportsValidLinkTypes(t *testing.T) {
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		switch {
		case request.URL.Path == "/rest/api/3/issueLinkType":
			return jiraJSONResponse(`{"issueLinkTypes":[{"name":"Blocks"},{"name":"Relates"}]}`)
		case request.Method == http.MethodPost:
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`No issue link type with name 'blocks' found`)),
			}
		default:
			return jiraJSONResponse(`{"fields":{"issuelinks":[]}}`)
		}
	})}

	_, err := NewWithClient(client, "user@example.com", "token").Execute(
		context.Background(),
		jiraTarget("https://jira.test"),
		linkOperation("audit", "WI-002", "WI-001", "blocks"),
		map[domain.ItemID]provider.RemoteRef{
			"WI-001": {Key: "DEVEX-7"},
			"WI-002": {Key: "DEVEX-8"},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "Blocks, Relates") {
		t.Fatalf("err = %v, want the instance's link types", err)
	}
}

func TestExecuteSkipsExistingIssueLink(t *testing.T) {
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if request.Method == http.MethodPost {
			t.Fatalf("unexpected link creation: %s %s", request.Method, request.URL.Path)
		}
		return jiraJSONResponse(
			`{"fields":{"issuelinks":[{"type":{"name":"Blocks"},"inwardIssue":{"key":"DEVEX-7"}}]}}`,
		)
	})}

	_, err := NewWithClient(client, "user@example.com", "token").Execute(
		context.Background(),
		jiraTarget("https://jira.test"),
		linkOperation("audit", "WI-002", "WI-001", defaultLinkType),
		map[domain.ItemID]provider.RemoteRef{
			"WI-001": {Key: "DEVEX-7"},
			"WI-002": {Key: "DEVEX-8"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteLinkRequiresPublishedItems(t *testing.T) {
	_, err := NewWithClient(nil, "user@example.com", "token").Execute(
		context.Background(),
		jiraTarget("https://jira.test"),
		linkOperation("audit", "WI-002", "WI-001", defaultLinkType),
		map[domain.ItemID]provider.RemoteRef{"WI-002": {Key: "DEVEX-8"}},
	)
	if err == nil || !strings.Contains(err.Error(), "WI-001") {
		t.Fatalf("err = %v, want unpublished WI-001", err)
	}
}

// Jira applies labels while creating an issue, so an unusable one must stop the plan instead of
// stranding a half-published bundle.
func TestPlanRejectsLabelsJiraCannotApply(t *testing.T) {
	_, _, err := NewWithClient(nil, "user@example.com", "token").Plan(
		context.Background(),
		&domain.WorkBreakdown{
			Discovery: domain.DiscoveryRef{ID: "audit", Document: "discovery.md"},
			Items: []domain.WorkItem{{
				ID:          "WI-001",
				Kind:        domain.KindTask,
				Title:       "First",
				Description: "First.",
				Labels:      []string{"needs review"},
			}},
		},
		nil,
		jiraTarget("https://jira.test"),
	)
	if err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("err = %v, want a whitespace label rejection", err)
	}
}

// An issue created in this run cannot already carry links, so no read should be spent on it.
func TestExecuteLinkTrustsIssuesCreatedThisRun(t *testing.T) {
	issueGets := 0
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/rest/api/3/issue/") {
			issueGets++
		}
		if request.Method == http.MethodPost && request.URL.Path == "/rest/api/3/issue" {
			return jiraJSONResponse(`{"id":"10042","key":"DEVEX-8"}`)
		}
		return jiraJSONResponse(`{"fields":{"issuelinks":[]}}`)
	})}
	adapter := NewWithClient(client, "user@example.com", "token")
	target := jiraTarget("https://jira.test")

	created, err := adapter.Execute(context.Background(), target, provider.Operation{
		ID: "create-WI-002",
		Fields: map[string]any{
			"project_key": "DEVEX", "issue_type": "Task", "title": "Second",
			"description": "Second.", "labels": []any{generatedLabel},
			"parent_item_id": "", "idempotency_marker": "audit/WI-002",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		target,
		linkOperation("audit", "WI-002", "WI-001", defaultLinkType),
		map[domain.ItemID]provider.RemoteRef{"WI-001": {Key: "DEVEX-7"}, "WI-002": created},
	); err != nil {
		t.Fatal(err)
	}
	if issueGets != 0 {
		t.Fatalf("issue reads = %d, want none for an issue created this run", issueGets)
	}
}

// Scoping the scan to one bundle keeps the search cost proportional to the bundle rather than to
// every issue this tool has created in the project.
func TestResolveScopesSearchToTheDiscoveryBundle(t *testing.T) {
	var queries []string
	client := &http.Client{Transport: jiraRoundTripFunc(func(request *http.Request) *http.Response {
		if request.URL.Path == "/rest/api/3/search/jql" {
			queries = append(queries, request.URL.Query().Get("jql"))
		}
		return jiraJSONResponse(`{"issues":[],"isLast":true}`)
	})}

	if _, err := NewWithClient(client, "user@example.com", "token").Resolve(
		context.Background(),
		jiraTarget("https://jira.test"),
		&provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: "audit/WI-001"}},
	); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("queries = %#v, want one search", queries)
	}
	if !strings.Contains(queries[0], `labels = "audit"`) ||
		!strings.Contains(queries[0], `labels = "`+generatedLabel+`"`) {
		t.Fatalf("jql = %q", queries[0])
	}
}

type jiraRoundTripFunc func(*http.Request) *http.Response

func (function jiraRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request), nil
}

func jiraJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func jiraTarget(baseURL string) config.Target {
	return config.Target{
		Provider: "jira",
		Jira: &config.JiraTarget{
			BaseURL:    baseURL,
			ProjectKey: "DEVEX",
		},
	}
}
