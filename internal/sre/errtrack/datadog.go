package errtrack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/project-init/gommon/pkg/datadog"
	gerror "github.com/project-init/gommon/pkg/errors"
)

// requestTimeout is per request. One issue lookup is a small read; a minute is
// the shared client's own default and more than enough.
const requestTimeout = 30 * time.Second

// issueFetcher reads one Error Tracking issue. The command depends on this
// rather than on a concrete client, so a test can answer from a fixture and
// never open a socket.
type issueFetcher interface {
	fetchIssue(ctx context.Context, issueID string) (issueResponse, error)
}

// datadogClient is the real issueFetcher, over the shared gommon client.
//
// Using gommon means the auth headers, the 429 retry honouring
// X-RateLimit-Reset, the refusal to follow redirects — Go would forward
// DD-API-KEY to the redirect target — and the capped error bodies all come for
// free and are tested there.
type datadogClient struct {
	dd      *datadog.Client
	baseURL string
}

func newDatadogClient(baseURL, apiKey, appKey string) *datadogClient {
	return &datadogClient{
		dd:      datadog.NewClient(&http.Client{Timeout: requestTimeout}, baseURL, apiKey, appKey),
		baseURL: baseURL,
	}
}

// issuePath is the Error Tracking issue endpoint with the query that makes the
// response useful.
//
// Built with url.Values rather than concatenation for two reasons: the issue id
// comes from a pasted link, and filter[samples.biases] contains brackets.
// Encode percent-encodes them to filter%5Bsamples.biases%5D, which Datadog
// honours — a real response came back reporting biases: ["git_metadata"]. If
// that ever stops being true the filter is ignored silently and samples arrive
// with no frames, which looks like missing data rather than a bad request.
func issuePath(issueID string) string {
	q := url.Values{}
	q.Set("include", "samples")
	q.Set("filter[samples.biases]", gitMetadataBias)

	return "/api/v2/error-tracking/issues/" + url.PathEscape(issueID) + "?" + q.Encode()
}

func (c *datadogClient) fetchIssue(ctx context.Context, issueID string) (issueResponse, error) {
	req, err := c.dd.NewRequest(ctx, http.MethodGet, issuePath(issueID), nil)
	if err != nil {
		return issueResponse{}, fmt.Errorf("build the issue request: %w", err)
	}

	var resp issueResponse
	if err := c.dd.Do(req, &resp); err != nil {
		return issueResponse{}, fmt.Errorf("fetch issue %s: %w", issueID, explain(err, c.site()))
	}

	return resp, nil
}

// site is the Datadog site this client talks to, for error messages. The
// client keeps the base URL private, so it is recovered the same way the
// command does.
func (c *datadogClient) site() string {
	return siteOfBaseURL(c.baseURL)
}

// explain turns Datadog's failures into what to do about them.
//
// Without this, a wrong site and a key missing a scope are both "HTTP 403",
// and a deleted issue and a typo in a pasted id are both "HTTP 404". The
// status alone sends people looking in the wrong place — the site is the
// likeliest cause of each, and it is the thing nobody thinks to check.
//
// gommon maps statuses onto its own sentinels, so these match on the sentinel
// rather than parsing a message. The original is wrapped, so the status and
// Datadog's own body are still there for anyone who wants them.
func explain(err error, site string) error {
	switch {
	case errors.Is(err, gerror.ErrPermissionDenied): // 401
		return fmt.Errorf("%w\nDatadog rejected the keys: check %s and %s, and that they belong to the %s org",
			err, apiKeyEnv, appKeyEnv, site)

	case errors.Is(err, gerror.ErrForbidden): // 403
		return fmt.Errorf("%w\nDatadog refused the request: the app key needs access to Error Tracking, and the keys must belong to the %s org",
			err, site)

	case errors.Is(err, gerror.ErrNotFound): // 404
		return fmt.Errorf("%w\nNo such issue on %s: it may have been deleted, or the link may be from a different Datadog org",
			err, site)

	case errors.Is(err, gerror.ErrTimeout):
		return fmt.Errorf("%w\nDatadog did not answer in time: retry, or check status.datadoghq.com", err)
	}

	return err
}
