package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// documentedIssue is GitHub's documented example issue, which is a pull request, changed by
// change.
func documentedIssue(t *testing.T, change func(issue map[string]any)) string {
	t.Helper()
	data, err := os.ReadFile("testdata/issue.golden")
	if err != nil {
		t.Fatal(err)
	}
	var issue map[string]any
	if err := json.Unmarshal(data, &issue); err != nil {
		t.Fatal(err)
	}
	change(issue)
	data, _ = json.Marshal(issue)
	return string(data)
}

// plainIssue makes the documented example an issue of the test repository carrying marker.
func plainIssue(marker string, labels ...string) func(map[string]any) {
	return func(issue map[string]any) {
		delete(issue, "pull_request")
		issue["repository_url"] = "https://api.github.test/repos/project-init/devex"
		issue["body"] = "Audit logs\n\n" + marker
		var list []any
		for _, name := range labels {
			list = append(list, map[string]any{"name": name})
		}
		issue["labels"] = list
	}
}

// numbered gives the documented example issue its number before change applies.
func numbered(number int, change func(map[string]any)) func(map[string]any) {
	return func(issue map[string]any) {
		change(issue)
		issue["number"] = number
	}
}

// canonicalRepository answers the repository read with GitHub's documented example at url.
func canonicalRepository(t *testing.T, url string) *http.Response {
	t.Helper()
	data, err := os.ReadFile("testdata/repository.golden")
	if err != nil {
		t.Fatal(err)
	}
	var repository map[string]any
	if err := json.Unmarshal(data, &repository); err != nil {
		t.Fatal(err)
	}
	repository["url"] = url
	data, _ = json.Marshal(repository)
	return jsonResponse(string(data))
}

const repositoryAPI = "https://api.github.test/repos/project-init/devex"

func TestConfirmReadsEachIssueByNumber(t *testing.T) {
	wi1, wi2, wi3, wi4, wi6 := marker("audit", "WI-001"), marker("audit", "WI-002"), marker("audit", "WI-003"), marker("audit", "WI-004"), marker("audit", "WI-006")
	var labeled []string
	client := newTestClient(t, func(request *http.Request) *http.Response {
		switch request.URL.Path {
		case "/repos/project-init/devex":
			return canonicalRepository(t, repositoryAPI)
		case "/repos/project-init/devex/issues/1":
			return jsonResponse(documentedIssue(t, numbered(1, plainIssue(wi1)))) // its label was removed
		case "/repos/project-init/devex/issues/1/labels":
			var names []string
			_ = json.NewDecoder(request.Body).Decode(&names)
			labeled = append(labeled, names...)
			return jsonResponse("[" + documentedLabel(t) + "]")
		case "/repos/project-init/devex/issues/2":
			return jsonResponse(documentedIssue(t, func(issue map[string]any) {
				pullRequest := issue["pull_request"]
				plainIssue(wi2)(issue)
				issue["pull_request"] = pullRequest // a pull request carrying the marker
			}))
		case "/repos/project-init/devex/issues/3":
			return jsonResponse(documentedIssue(t, func(issue map[string]any) {
				plainIssue(wi3)(issue)
				issue["repository_url"] = "https://api.github.test/repos/project-init/elsewhere" // transferred
			}))
		case "/repos/project-init/devex/issues/6":
			return jsonResponse(documentedIssue(t, numbered(6, plainIssue(wi6, "discovery:audit")))) // still labeled
		case "/repos/project-init/devex/issues/6/labels":
			t.Error("relabeled an issue that kept its discovery label")
			return jsonResponse("[]")
		case "/repos/project-init/devex/issues/4":
			return jsonResponse(documentedIssue(t, plainIssue("someone rewrote the body"))) // marker gone
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`))}
		}
	})
	confirmed, err := NewWithClient(client).Confirm(context.Background(), githubTarget(), map[string]provider.RemoteRef{
		wi1: {Key: "1"}, wi2: {Key: "2"}, wi3: {Key: "3"}, wi4: {Key: "4"}, marker("audit", "WI-005"): {Key: "5"}, wi6: {Key: "6"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok1 := confirmed[wi1]; !ok1 || len(confirmed) != 2 {
		t.Errorf("Confirm() = %#v, want only the plain issues that still carry their markers", confirmed)
	}
	if _, ok6 := confirmed[wi6]; !ok6 {
		t.Errorf("Confirm() lacks WI-006")
	}
	if strings.Join(labeled, ",") != "discovery:audit" {
		t.Errorf("labels added = %q, want the discovery label restored", labeled)
	}
}

func TestConfirmFailsWhenGitHubCannotAnswer(t *testing.T) {
	client := newTestClient(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{"message":"Server Error"}`))}
	})
	if _, err := NewWithClient(client).Confirm(context.Background(), githubTarget(), map[string]provider.RemoteRef{marker("audit", "WI-001"): {Key: "1"}}); err == nil {
		t.Error("Confirm() = nil error, want the failed read reported rather than the issue treated as gone")
	}
}

func TestConfirmStopsLabelingAfterTheFirstRefusal(t *testing.T) {
	attempts := 0
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.URL.Path == "/repos/project-init/devex" {
			return canonicalRepository(t, repositoryAPI)
		}
		if strings.HasSuffix(request.URL.Path, "/labels") {
			attempts++
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"Must have admin rights to Repository."}`))}
		}
		number := strings.TrimPrefix(request.URL.Path, "/repos/project-init/devex/issues/")
		n, _ := strconv.Atoi(number)
		return jsonResponse(documentedIssue(t, numbered(n, plainIssue(marker("audit", domain.ItemID("WI-00"+number))))))
	})
	candidates := map[string]provider.RemoteRef{}
	for _, item := range []string{"1", "2", "3"} {
		candidates[marker("audit", domain.ItemID("WI-00"+item))] = provider.RemoteRef{Key: item}
	}
	confirmed, err := NewWithClient(client).Confirm(context.Background(), githubTarget(), candidates)
	if err != nil || len(confirmed) != 3 {
		t.Fatalf("Confirm() = %d confirmed, %v; want all three despite the refused labels", len(confirmed), err)
	}
	if attempts != 1 {
		t.Errorf("label attempts = %d, want one before giving up", attempts)
	}
}

func TestConfirmMatchesARenamedRepository(t *testing.T) {
	renamed := "https://api.github.test/repos/project-init/devex-cli"
	wi1 := marker("audit", "WI-001")
	client := newTestClient(t, func(request *http.Request) *http.Response {
		switch request.URL.Path {
		case "/repos/project-init/devex": // GitHub redirects the old name
			return canonicalRepository(t, renamed)
		case "/repos/project-init/devex/issues/1/labels":
			return jsonResponse("[]")
		default:
			return jsonResponse(documentedIssue(t, numbered(1, func(issue map[string]any) {
				plainIssue(wi1)(issue)
				issue["repository_url"] = renamed
			})))
		}
	})
	confirmed, err := NewWithClient(client).Confirm(context.Background(), githubTarget(), map[string]provider.RemoteRef{wi1: {Key: "1"}})
	if err != nil || len(confirmed) != 1 {
		t.Errorf("Confirm() = %v, %v; want the issue of the renamed repository", confirmed, err)
	}
}

func TestConfirmFailsOnARefusedRead(t *testing.T) {
	// A 403 means the token was refused, not that the issue is gone.
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.URL.Path == "/repos/project-init/devex" {
			return canonicalRepository(t, repositoryAPI)
		}
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"Resource not accessible by integration"}`))}
	})
	if _, err := NewWithClient(client).Confirm(context.Background(), githubTarget(), map[string]provider.RemoteRef{marker("audit", "WI-001"): {Key: "1"}}); err == nil {
		t.Error("Confirm() = nil error, want the refusal reported rather than the issue treated as gone")
	}
}
