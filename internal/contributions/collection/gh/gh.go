package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v74/github"

	"github.com/project-init/gommon/pkg/githubclient"
)

type GH struct {
	ghClient     *github.Client
	organization string
	warn         func(format string, args ...any)

	mu          sync.Mutex
	warnedReset time.Time // the rate-limit reset warnIfSpent last warned about
}

// New reads organization through an authenticated client, reporting waits through warn.
func New(organization string, warn func(format string, args ...any)) (*GH, error) {
	ghToken, err := githubclient.ResolveToken()
	if err != nil {
		return nil, githubAuthError(err)
	}

	ghClient, err := githubclient.New(ghToken, "")
	if err != nil {
		return nil, err
	}

	return &GH{
		ghClient:     ghClient,
		organization: organization,
		warn:         warn,
	}, nil
}

func githubAuthError(err error) error {
	if errors.Is(err, githubclient.ErrAuthUnavailable) {
		return fmt.Errorf("GitHub auth required (run `gh auth login` or set GITHUB_TOKEN)")
	}
	return err
}

// graphQLAttempts bounds the requests graphQL makes for one query.
const graphQLAttempts = 3

// Waits between graphQL's attempts; tests shorten them. A failure pauses retryPause, doubled for
// the third attempt. A spent limit waits for its reset plus resetBuffer, which absorbs a local
// clock running ahead of GitHub's. A secondary limit waits for Retry-After, or secondaryPause.
var (
	retryPause     = 2 * time.Second
	resetBuffer    = 5 * time.Second
	secondaryPause = time.Minute
)

// graphQL runs query against GitHub's GraphQL API through the REST client, so requests share its
// authentication and its retries of failed responses, and decodes the response's data into out.
// GitHub reports GraphQL rate limits inside a 200 response, which those retries never see, so
// graphQL waits them out itself. It also retries what the transport passes through untried, such
// as a timeout or an untyped GraphQL error.
func (g *GH) graphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	// Another worker's spent limit makes go-github hold a request until the reset rather than fail.
	ctx = context.WithValue(ctx, github.SleepUntilPrimaryRateLimitResetWhenRateLimited, true)
	for attempt := 1; ; attempt++ {
		rate, wait, retryable, err := g.graphQLOnce(ctx, query, variables, out)
		if err != nil && ctx.Err() != nil {
			return ctx.Err() // rather than go-github's wording for a hold cut short
		}
		if err == nil || retryable {
			g.warnIfSpent(rate)
		}
		if err == nil || !retryable || attempt == graphQLAttempts {
			return err
		}
		if wait == 0 {
			wait = retryPause << (attempt - 1)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(wait, time.Hour)):
		}
	}
}

// graphQLOnce runs query once. It returns the rate limit the response reported, whether a failure
// is worth another attempt, and how long to wait first when GitHub says.
func (g *GH) graphQLOnce(ctx context.Context, query string, variables map[string]any, out any) (rate github.Rate, wait time.Duration, retryable bool, err error) {
	request, err := g.ghClient.NewRequest(http.MethodPost, "graphql", map[string]any{"query": query, "variables": variables})
	if err != nil {
		return rate, 0, false, err
	}
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type       string `json:"type"`
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	httpResponse, err := g.ghClient.Do(ctx, request, &response)
	if httpResponse != nil {
		rate = httpResponse.Rate
	}
	if err != nil {
		return rate, 0, !retriedByTransport(err), err
	}
	if len(response.Errors) == 0 {
		return rate, 0, false, json.Unmarshal(response.Data, out)
	}
	// An error with a type, such as NOT_FOUND, or a code, such as a schema violation, is permanent;
	// a timeout arrives with neither. RATE_LIMITED is the exception.
	messages := make([]string, len(response.Errors))
	transient, limited := true, false
	for index, graphQLError := range response.Errors {
		messages[index] = graphQLError.Message
		limited = limited || graphQLError.Type == "RATE_LIMITED"
		transient = transient && (graphQLError.Type == "" && graphQLError.Extensions.Code == "" || graphQLError.Type == "RATE_LIMITED")
	}
	err = fmt.Errorf("GitHub GraphQL: %s", strings.Join(messages, "; "))
	switch {
	case !transient:
		return rate, 0, false, err
	case rate.Remaining == 0 && !rate.Reset.IsZero():
		// GitHub documents a spent primary limit as errors with x-ratelimit-remaining 0.
		return rate, max(time.Until(rate.Reset.Time), 0) + resetBuffer, true, err
	case limited:
		// A secondary limit: GitHub says to honor Retry-After, or else wait at least a minute.
		if seconds, parseErr := strconv.Atoi(httpResponse.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
			return rate, time.Duration(seconds) * time.Second, true, err
		}
		return rate, secondaryPause, true, err
	default:
		return rate, 0, true, err
	}
}

// retriedByTransport reports whether err is an HTTP error that gommon's transport and go-github
// have already retried or waited out.
func retriedByTransport(err error) bool {
	var failed *github.ErrorResponse
	var limited *github.RateLimitError
	var abused *github.AbuseRateLimitError
	return errors.As(err, &failed) || errors.As(err, &limited) || errors.As(err, &abused)
}

// warnIfSpent warns once per reset that a spent rate limit will pause collection. go-github
// waits for the reset before sending the next request, without a word.
func (g *GH) warnIfSpent(rate github.Rate) {
	if g.warn == nil || rate.Remaining != 0 || rate.Reset.IsZero() || !time.Now().Before(rate.Reset.Time) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if rate.Reset.Time.Equal(g.warnedReset) {
		return
	}
	g.warnedReset = rate.Reset.Time
	g.warn("GitHub's GraphQL rate limit is spent; waiting until %s", rate.Reset.Local().Format(time.TimeOnly))
}
