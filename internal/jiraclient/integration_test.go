package jiraclient_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/project-init/devex/internal/jiraclient"
)

func TestLiveJiraAPI(t *testing.T) {
	email := os.Getenv("JIRA_EMAIL")
	apiKey := os.Getenv("JIRA_API_KEY")
	baseURL := os.Getenv("JIRA_URL") // e.g. https://my-domain.atlassian.net

	if email == "" || apiKey == "" || baseURL == "" {
		t.Skip("Skipping live Jira integration test: JIRA_EMAIL, JIRA_API_KEY, and JIRA_URL must be set")
	}

	// Wait up to 10 seconds per request
	client := jiraclient.NewClient(&http.Client{Timeout: 10 * time.Second}, baseURL, email, apiKey)
	
	ctx := context.Background()

	// Simple read-only check: Fetch link types
	types, err := client.GetIssueLinkTypes(ctx)
	if err != nil {
		t.Fatalf("Failed to get issue link types: %v", err)
	}

	if len(types) == 0 {
		t.Log("Warning: Jira instance has no link types defined.")
	} else {
		t.Logf("Successfully fetched %d link types. e.g. %s", len(types), types[0])
	}
}
