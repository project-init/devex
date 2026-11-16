package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v74/github"
)

// graphQLServer answers each GraphQL request with the next of responses, then with an empty last
// page, as the catch-up pass sees when nothing changed mid-scan. It records each request's
// variables.
func graphQLServer(t *testing.T, responses ...string) (*GH, *[]map[string]any) {
	t.Helper()
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Errorf("request = %s %s, want the GraphQL endpoint", r.Method, r.URL.Path)
		}
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body.Variables)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) > len(responses) {
			_, _ = io.WriteString(w, pullRequestPage(false))
			return
		}
		_, _ = io.WriteString(w, responses[len(requests)-1])
	}))
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	return &GH{ghClient: client, organization: "project-init"}, &requests
}

// pullRequestPage renders a pullRequests page in the shape testdata/pull_requests.golden
// captures from GitHub.
func pullRequestPage(hasNextPage bool, nodes ...string) string {
	return fmt.Sprintf(`{"data":{"repository":{"pullRequests":{"pageInfo":{"hasNextPage":%t,"endCursor":"page-2"},"nodes":[%s]}}}}`,
		hasNextPage, strings.Join(nodes, ","))
}

func pullRequestNodeJSON(number int, author, createdAt, mergedAt, updatedAt string, reviewsHaveNextPage bool, reviews ...string) string {
	return fmt.Sprintf(`{"number":%d,"createdAt":%q,"mergedAt":%q,"updatedAt":%q,"author":%s,"reviews":{"pageInfo":{"hasNextPage":%t,"endCursor":"reviews-2"},"nodes":[%s]}}`,
		number, createdAt, mergedAt, updatedAt, author, reviewsHaveNextPage, strings.Join(reviews, ","))
}

func user(login string) string { return fmt.Sprintf(`{"__typename":"User","login":%q}`, login) }
func bot(login string) string  { return fmt.Sprintf(`{"__typename":"Bot","login":%q}`, login) }

func review(author, state string) string {
	return fmt.Sprintf(`{"author":%s,"state":%q}`, author, state)
}

var cutoff = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

func TestPullRequestsResponseReadsGitHubsShape(t *testing.T) {
	data, err := os.ReadFile("testdata/pull_requests.golden")
	if err != nil {
		t.Fatal(err)
	}
	// The captured page says more follow; the server's next page is empty.
	g, _ := graphQLServer(t, string(data))
	prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	if err != nil {
		t.Fatal(err)
	}
	want := "octocat #115 merged 2026-11-13T20:46:55Z after 23m35s: monalisa:APPROVED!dependabot[bot]:COMMENTED"
	if len(prs) != 1 {
		t.Fatalf("prs = %#v, want one", prs)
	}
	pr := prs[0]
	if got := fmt.Sprintf("%s #%d merged %s after %s: %s", pr.Author, pr.Number, pr.MergedAt.Format(time.RFC3339), pr.TimeToMerge, pr.Reviews); got != want {
		t.Errorf("pr = %s\nwant  %s", got, want)
	}
}

func TestGetRepoPRsKeepsEveryReview(t *testing.T) {
	g, requests := graphQLServer(t,
		pullRequestPage(false, pullRequestNodeJSON(7, user("octocat"), "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "2026-11-02T00:00:00Z", true,
			review(user("monalisa"), "COMMENTED"), review(user("hubot"), "COMMENTED"))),
		`{"data":{"repository":{"pullRequest":{"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":"end"},"nodes":[`+review(user("monalisa"), "APPROVED")+`]}}}}}`,
	)
	prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Reviews != "monalisa:COMMENTED!hubot:COMMENTED!monalisa:APPROVED" {
		t.Errorf("prs = %#v, want all three reviews", prs)
	}
	if after := (*requests)[1]["after"]; after != "reviews-2" {
		t.Errorf("second review page after = %v, want the first page's cursor", after)
	}
}

func TestGetRepoPRsCollectsByMergeDate(t *testing.T) {
	g, _ := graphQLServer(t, pullRequestPage(false,
		// Opened long before the window, merged inside it.
		pullRequestNodeJSON(1, user("octocat"), "2026-01-05T00:00:00Z", "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z", false),
		// Merged before the window, commented on since.
		pullRequestNodeJSON(2, user("octocat"), "2026-07-01T00:00:00Z", "2026-07-02T00:00:00Z", "2026-09-03T00:00:00Z", false),
	))
	prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 1 {
		t.Errorf("prs = %#v, want only #1, merged inside the window", prs)
	}
}

func TestGetRepoPRsStopsOnceAPageReachesTheCutoff(t *testing.T) {
	g, requests := graphQLServer(t, pullRequestPage(true,
		pullRequestNodeJSON(3, user("octocat"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false),
		pullRequestNodeJSON(4, user("octocat"), "2026-06-01T00:00:00Z", "2026-06-02T00:00:00Z", "2026-06-02T00:00:00Z", false),
	))
	if prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex"); err != nil || len(prs) != 1 {
		t.Errorf("GetRepoPRs() = %#v, %v", prs, err)
	}
	// One page used no cursor, so nothing could jump behind one: no catch-up pass.
	if len(*requests) != 1 {
		t.Errorf("requests = %v, want one page", *requests)
	}
}

func TestGetRepoPRsCatchesAPullRequestUpdatedMidScan(t *testing.T) {
	now := time.Now().UTC()
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	g, requests := graphQLServer(t,
		pullRequestPage(true, pullRequestNodeJSON(20, user("octocat"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false)),
		pullRequestPage(false, pullRequestNodeJSON(19, user("octocat"), "2026-08-30T00:00:00Z", "2026-08-31T00:00:00Z", "2026-08-31T00:00:00Z", false)),
		// The catch-up pass: #21 was commented on during the scan and jumped behind the cursor.
		// #22 changed five minutes ago, inside the clock-skew margin, so the pass reads on.
		pullRequestPage(true,
			pullRequestNodeJSON(21, user("monalisa"), "2026-08-20T00:00:00Z", "2026-08-21T00:00:00Z", at(0), false),
			pullRequestNodeJSON(22, user("monalisa"), "2026-08-22T00:00:00Z", "2026-08-23T00:00:00Z", at(5*time.Minute), false)),
		// It stops at #20, seen already and last updated before the scan, though more pages follow.
		pullRequestPage(true, pullRequestNodeJSON(20, user("octocat"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false)),
	)
	prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	var numbers []int
	for _, pr := range prs {
		numbers = append(numbers, pr.Number)
	}
	if err != nil || fmt.Sprint(numbers) != "[20 19 21 22]" {
		t.Errorf("GetRepoPRs() = %v, %v; want #20 once, then #21 and #22 from the catch-up pass", numbers, err)
	}
	if len(*requests) != 4 {
		t.Errorf("requests = %d, want two pages and two catch-up pages", len(*requests))
	}
}

func TestGetRepoPRsCatchUpStopsAtTheCutoff(t *testing.T) {
	// A rerun minutes after the last merge: the cutoff is later than the scan's start minus the
	// margin, so the catch-up pass stops at the cutoff.
	now := time.Now().UTC()
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	g, requests := graphQLServer(t,
		pullRequestPage(true, pullRequestNodeJSON(30, user("octocat"), at(time.Hour), at(30*time.Second), at(30*time.Second), false)),
		pullRequestPage(false),
		pullRequestPage(true,
			pullRequestNodeJSON(30, user("octocat"), at(time.Hour), at(30*time.Second), at(30*time.Second), false),
			pullRequestNodeJSON(31, user("octocat"), at(time.Hour), at(50*time.Minute), at(3*time.Minute), false)),
	)
	if _, err := g.GetRepoPRs(context.Background(), now.Add(-time.Minute), "devex"); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 3 {
		t.Errorf("requests = %d, want the catch-up pass to stop at #31, updated before the cutoff", len(*requests))
	}
}

func TestGetRepoPRsReadsTheNextPage(t *testing.T) {
	g, requests := graphQLServer(t,
		pullRequestPage(true, pullRequestNodeJSON(5, user("octocat"), "2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z", "2026-10-02T00:00:00Z", false)),
		pullRequestPage(false, pullRequestNodeJSON(6, user("octocat"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false)),
	)
	prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	if err != nil || len(prs) != 2 || (*requests)[1]["after"] != "page-2" {
		t.Errorf("GetRepoPRs() = %#v, %v; requests %v", prs, err, *requests)
	}
}

func TestGetRepoPRsNamesBotsAsRESTDoes(t *testing.T) {
	g, _ := graphQLServer(t, pullRequestPage(false,
		pullRequestNodeJSON(8, bot("github-actions"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false),
		pullRequestNodeJSON(9, user("octocat"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false,
			review(bot("dependabot"), "COMMENTED"), review("null", "APPROVED")),
	))
	prs, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	if err != nil {
		t.Fatal(err)
	}
	// github-actions[bot] pull requests are skipped; a deleted reviewer shows as ghost.
	if len(prs) != 1 || prs[0].Reviews != "dependabot[bot]:COMMENTED!ghost:APPROVED" {
		t.Errorf("prs = %#v", prs)
	}
}

func TestGraphQLGivesUpAfterThreeAttempts(t *testing.T) {
	pause := retryPause
	retryPause = 20 * time.Millisecond
	t.Cleanup(func() { retryPause = pause })
	response := `{"data":null,"errors":[{"message":"Something went wrong"}]}`
	g, requests := graphQLServer(t, response, response, response)
	start := time.Now()
	_, err := g.GetRepoPRs(context.Background(), cutoff, "devex")
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Errorf("three attempts took %s, want pauses of 20ms and 40ms between them", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "Something went wrong") {
		t.Errorf("GetRepoPRs() error = %v, want the GraphQL error", err)
	}
	if len(*requests) != 3 {
		t.Errorf("requests = %d, want three attempts", len(*requests))
	}
}

func TestGraphQLLeavesHTTPErrorsToTheTransport(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	if _, err := (&GH{ghClient: client, organization: "project-init"}).GetRepoPRs(context.Background(), cutoff, "devex"); err == nil || calls != 1 {
		t.Errorf("GetRepoPRs() = %v after %d calls; want one call, since the transport already retried", err, calls)
	}
}

// shortenWaits sets graphQL's waits for one test, so a test can tell which wait applied.
func shortenWaits(t *testing.T, retry, buffer, secondary time.Duration) {
	t.Helper()
	saved := []time.Duration{retryPause, resetBuffer, secondaryPause}
	retryPause, resetBuffer, secondaryPause = retry, buffer, secondary
	t.Cleanup(func() { retryPause, resetBuffer, secondaryPause = saved[0], saved[1], saved[2] })
}

// boundedContext ends after ten seconds, so a test that waits for the wrong thing fails on its
// assertion instead of timing out.
func boundedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// headerServer answers each request with the next of responses and headers, then with an empty
// last page, and counts requests.
func headerServer(t *testing.T, headers []map[string]string, responses ...string) (*GH, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls > len(responses) {
			_, _ = io.WriteString(w, pullRequestPage(false))
			return
		}
		for key, value := range headers[calls-1] {
			w.Header().Set(key, value)
		}
		_, _ = io.WriteString(w, responses[calls-1])
	}))
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	return &GH{ghClient: client, organization: "project-init"}, &calls
}

func TestGraphQLHonorsASecondaryLimitsRetryAfter(t *testing.T) {
	shortenWaits(t, time.Hour, 0, time.Hour)
	g, calls := headerServer(t, []map[string]string{{"X-RateLimit-Remaining": "4000", "Retry-After": "1"}},
		`{"errors":[{"type":"RATE_LIMITED","message":"You have exceeded a secondary rate limit."}]}`)
	start := time.Now()
	if _, err := g.GetRepoPRs(boundedContext(t), cutoff, "devex"); err != nil || *calls != 2 {
		t.Fatalf("GetRepoPRs() = %v after %d calls; want a retry", err, *calls)
	}
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 30*time.Second {
		t.Errorf("retried after %s, want Retry-After's second", elapsed)
	}
}

func TestGraphQLWaitsAMinuteForASecondaryLimitWithoutRetryAfter(t *testing.T) {
	shortenWaits(t, time.Hour, 0, 50*time.Millisecond)
	g, calls := headerServer(t, []map[string]string{{"X-RateLimit-Remaining": "4000"}},
		`{"errors":[{"type":"RATE_LIMITED","message":"You have exceeded a secondary rate limit."}]}`)
	if _, err := g.GetRepoPRs(boundedContext(t), cutoff, "devex"); err != nil || *calls != 2 {
		t.Errorf("GetRepoPRs() = %v after %d calls; want a retry after secondaryPause", err, *calls)
	}
}

func TestGraphQLDoesNotWaitOutAPermanentErrorThatSpentTheLimit(t *testing.T) {
	shortenWaits(t, time.Hour, time.Hour, time.Hour)
	g, calls := headerServer(t, []map[string]string{{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(time.Now().Add(time.Hour).Unix())}},
		`{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"locations":[{"line":1,"column":42}],"message":"Could not resolve to a Repository with the name 'project-init/missing'."}]}`)
	var warnings []string
	g.warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	if _, err := g.GetRepoPRs(boundedContext(t), cutoff, "missing"); err == nil || !strings.Contains(err.Error(), "Could not resolve") || *calls != 1 {
		t.Errorf("GetRepoPRs() = %v after %d calls; want the NOT_FOUND error at once", err, *calls)
	}
	if len(warnings) > 0 {
		t.Errorf("warnings = %q, want none: nothing waits", warnings)
	}
}

func TestGraphQLWaitsOutTheRateLimit(t *testing.T) {
	// A retry on the backoff would wait an hour; only the reset's wait finishes in time.
	shortenWaits(t, time.Hour, 0, time.Hour)
	calls := 0
	reset := time.Now().Truncate(time.Second).Add(2 * time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			// GitHub documents a spent GraphQL limit as status 200 with an error message and
			// x-ratelimit-remaining 0, retried after x-ratelimit-reset. It gives no example body.
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
			_, _ = io.WriteString(w, `{"errors":[{"message":"API rate limit exceeded"}]}`)
			return
		}
		_, _ = io.WriteString(w, pullRequestPage(false))
	}))
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	var warnings []string
	g := &GH{ghClient: client, organization: "project-init", warn: func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}}
	_, err := g.GetRepoPRs(boundedContext(t), cutoff, "devex")
	if err != nil || calls != 2 {
		t.Errorf("GetRepoPRs() = %v after %d calls; want a retry once the limit resets", err, calls)
	}
	if time.Now().Before(reset) {
		t.Errorf("retried at %s, before the reset at %s", time.Now().Format(time.TimeOnly), reset.Format(time.TimeOnly))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "rate limit") {
		t.Errorf("warnings = %q, want one about the wait", warnings)
	}
}

func TestGraphQLDoesNotRetryASchemaError(t *testing.T) {
	// GitHub's response, captured, for a query naming a field the schema lacks.
	g, requests := graphQLServer(t, `{"errors":[{"path":["query","repository","nope"],"extensions":{"code":"undefinedField","typeName":"Repository","fieldName":"nope"},"locations":[{"line":1,"column":60}],"message":"Field 'nope' doesn't exist on type 'Repository'"}]}`)
	if _, err := g.GetRepoPRs(context.Background(), cutoff, "devex"); err == nil || len(*requests) != 1 {
		t.Errorf("GetRepoPRs() = %v after %d requests; want one", err, len(*requests))
	}
}

func TestGraphQLDoesNotRetryAPermanentError(t *testing.T) {
	// GitHub's response, captured, for a repository that does not exist.
	g, requests := graphQLServer(t, `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"locations":[{"line":1,"column":42}],"message":"Could not resolve to a Repository with the name 'project-init/missing'."}]}`)
	if _, err := g.GetRepoPRs(context.Background(), cutoff, "missing"); err == nil || len(*requests) != 1 {
		t.Errorf("GetRepoPRs() = %v after %d requests; want one", err, len(*requests))
	}
}

func TestGraphQLLeavesSecondaryLimitsToTheTransport(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again.","documentation_url":"https://docs.github.com/rest/overview/rate-limits-for-the-rest-api#about-secondary-rate-limits"}`)
	}))
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	if _, err := (&GH{ghClient: client, organization: "project-init"}).GetRepoPRs(context.Background(), cutoff, "devex"); err == nil || calls != 1 {
		t.Errorf("GetRepoPRs() = %v after %d calls; want one", err, calls)
	}
}

func TestWarnIfSpentWarnsOncePerReset(t *testing.T) {
	var warnings []string
	g := &GH{warn: func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }}
	spent := github.Rate{Limit: 5000, Remaining: 0, Reset: github.Timestamp{Time: time.Now().Add(time.Hour)}}
	g.warnIfSpent(github.Rate{Limit: 5000, Remaining: 12, Reset: spent.Reset})
	g.warnIfSpent(spent)
	g.warnIfSpent(spent) // another worker's response
	g.warnIfSpent(github.Rate{Limit: 5000, Remaining: 0, Reset: github.Timestamp{Time: time.Now().Add(-time.Minute)}})
	if len(warnings) != 1 {
		t.Errorf("warnings = %q, want one for the spent limit", warnings)
	}
	g.warnIfSpent(github.Rate{Limit: 5000, Remaining: 0, Reset: github.Timestamp{Time: time.Now().Add(2 * time.Hour)}})
	if len(warnings) != 2 {
		t.Errorf("warnings = %q, want another for the next spent limit", warnings)
	}
}

func TestGetRepoPRsWarnsWhenASuccessSpendsTheLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		_, _ = io.WriteString(w, pullRequestPage(false))
	}))
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(server.URL + "/")
	var warnings []string
	g := &GH{ghClient: client, organization: "project-init", warn: func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}}
	if _, err := g.GetRepoPRs(context.Background(), cutoff, "devex"); err != nil || len(warnings) != 1 {
		t.Errorf("GetRepoPRs() = %v with warnings %q; want one warning before go-github's silent wait", err, warnings)
	}
}

func TestGraphQLReportsCancellationDuringARateLimitHold(t *testing.T) {
	// The first page spends the limit, so go-github holds the second until the reset.
	g, _ := headerServer(t, []map[string]string{{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(time.Now().Add(time.Hour).Unix())}},
		pullRequestPage(true, pullRequestNodeJSON(40, user("octocat"), "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-02T00:00:00Z", false)))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.GetRepoPRs(ctx, cutoff, "devex"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("GetRepoPRs() error = %v, want the context's", err)
	}
}
