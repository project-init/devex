package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/config"
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
				"labels":             []any{"devex-discovery"},
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

func TestLookupFindsJiraProperty(t *testing.T) {
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
	adapter := NewWithClient(client, "user@example.com", "token")
	remote, err := adapter.Lookup(
		context.Background(),
		jiraTarget("https://jira.test"),
		"audit/WI-001",
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote == nil || remote.Key != "DEVEX-7" {
		t.Fatalf("remote = %#v", remote)
	}
	second, err := adapter.Lookup(
		context.Background(),
		jiraTarget("https://jira.test"),
		"audit/WI-002",
	)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil || second.Key != "DEVEX-8" {
		t.Fatalf("second = %#v", second)
	}
	if searchRequests != 2 {
		t.Fatalf("search requests = %d, want 2", searchRequests)
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
