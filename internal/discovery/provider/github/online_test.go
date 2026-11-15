package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// repositoryServer answers the repository read with GitHub's documented example, with change
// applied to it, or with status when it is set.
func repositoryServer(t *testing.T, change map[string]any, status int) *Adapter {
	t.Helper()
	data, err := os.ReadFile("testdata/repository.golden")
	if err != nil {
		t.Fatal(err)
	}
	if len(change) > 0 {
		var repository map[string]any
		if err := json.Unmarshal(data, &repository); err != nil {
			t.Fatal(err)
		}
		for key, value := range change {
			repository[key] = value
		}
		if data, err = json.Marshal(repository); err != nil {
			t.Fatal(err)
		}
	}
	return NewWithClient(newTestClient(t, func(request *http.Request) *http.Response {
		if request.URL.Path != "/repos/project-init/devex" {
			t.Fatalf("request = %s", request.URL.Path)
		}
		if status != 0 {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"message":"Bad credentials"}`))}
		}
		return jsonResponse(string(data))
	}))
}

func TestCheckAccessReadsTheRepository(t *testing.T) {
	if detail, err := repositoryServer(t, nil, 0).CheckAccess(context.Background(), githubTarget()); err != nil || !strings.Contains(detail, "takes issues") {
		t.Errorf("CheckAccess() = %q, %v", detail, err)
	}
}

func TestCheckAccessFailsOnBadCredentials(t *testing.T) {
	if _, err := repositoryServer(t, nil, http.StatusUnauthorized).CheckAccess(context.Background(), githubTarget()); err == nil {
		t.Error("CheckAccess() accepted a refused token")
	}
}

func TestCheckAccessFailsWithIssuesOff(t *testing.T) {
	_, err := repositoryServer(t, map[string]any{"has_issues": false}, 0).CheckAccess(context.Background(), githubTarget())
	if err == nil || !strings.Contains(err.Error(), "issues turned off") {
		t.Errorf("CheckAccess() error = %v, want issues turned off", err)
	}
}

func TestCheckAccessFailsOnAnArchivedRepository(t *testing.T) {
	_, err := repositoryServer(t, map[string]any{"archived": true}, 0).CheckAccess(context.Background(), githubTarget())
	if err == nil || !strings.Contains(err.Error(), "is archived") {
		t.Errorf("CheckAccess() error = %v, want the archive named", err)
	}
}
