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

	mu       sync.Mutex
	resumeAt time.Time // when a rate limit lets every worker send GraphQL queries again
	strikes  int       // fallback pauses in a row for queries sent after the last pause lifted
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

// graphQLAttempts bounds the requests graphQL sends for a query that keeps failing;
// limitedReplies bounds the rate-limited replies it accepts for one query.
const (
	graphQLAttempts = 3
	limitedReplies  = 6
)

// Waits between graphQL's requests; tests shorten them. A failure pauses retryPause, doubled for
// the third attempt. A rate limit waits for Retry-After or for a spent limit's reset plus
// resetBuffer, which absorbs a local clock running ahead of GitHub's, whichever is later, capped
// at an hour. Without either, it waits secondaryPause, doubling for each fallback pause in a row
// up to maxSecondaryPause.
var (
	retryPause        = 2 * time.Second
	resetBuffer       = 5 * time.Second
	secondaryPause    = time.Minute
	maxSecondaryPause = 16 * time.Minute
)

// graphQL runs query against GitHub's GraphQL API through the REST client, so requests share its
// authentication and its retries of failed responses, and decodes the response's data into out.
// GitHub reports GraphQL rate limits inside a 200 response, which those retries never see, so
// graphQL waits them out itself, and pauses every worker's queries until the limit lifts, since
// GitHub counts concurrent requests toward its secondary limits. It also retries what the
// transport passes through untried, such as a timeout or an untyped GraphQL error.
func (g *GH) graphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	failures, limited := 0, 0
	for {
		if err := g.awaitResume(ctx); err != nil {
			return err
		}
		sent := time.Now()
		result, err := g.graphQLOnce(ctx, query, variables, out)
		if err != nil && ctx.Err() != nil {
			return ctx.Err() // rather than the transport's wording for a request cut short
		}
		switch {
		case err == nil:
			g.lifted(sent, result.rate)
			return nil
		case result.limited:
			if limited++; limited == limitedReplies {
				return fmt.Errorf("%w (rate-limited %d times)", err, limited)
			}
			g.pause(sent, result)
		case !result.retryable:
			return err
		default:
			if failures++; failures == graphQLAttempts {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryPause << (failures - 1)):
			}
		}
	}
}

// reply is what one GraphQL request learned beyond its data.
type reply struct {
	rate       github.Rate   // the rate limit the response reported
	limited    bool          // GitHub refused the query for a rate limit
	retryAfter time.Duration // GitHub's Retry-After for a refused query, or zero
	retryable  bool          // another attempt may succeed
}

// graphQLOnce runs query once.
func (g *GH) graphQLOnce(ctx context.Context, query string, variables map[string]any, out any) (reply, error) {
	var result reply
	request, err := g.ghClient.NewRequest(http.MethodPost, "graphql", map[string]any{"query": query, "variables": variables})
	if err != nil {
		return result, err
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
		result.rate = httpResponse.Rate
	}
	var spent *github.RateLimitError
	switch {
	case errors.As(err, &spent):
		// go-github refuses to send while its last response reported the primary limit spent, and
		// the transport gives up on one that stays spent; either way, pause until the reset.
		result.limited, result.retryable = true, true
		return result, err
	case err != nil:
		result.retryable = !retriedByTransport(err)
		return result, err
	}
	if len(response.Errors) == 0 {
		return result, json.Unmarshal(response.Data, out)
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
	if !transient {
		return result, err
	}
	result.retryable = true
	// GitHub documents a spent primary limit as errors with x-ratelimit-remaining 0.
	result.limited = limited || result.rate.Remaining == 0 && !result.rate.Reset.IsZero()
	if seconds, parseErr := strconv.Atoi(httpResponse.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
		result.retryAfter = time.Duration(seconds) * time.Second
	}
	return result, err
}

func (g *GH) awaitResume(ctx context.Context) error {
	for {
		g.mu.Lock()
		wait := time.Until(g.resumeAt)
		g.mu.Unlock()
		if wait <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// pause holds every worker's queries after GitHub refuses a query sent at sent. It follows
// GitHub's guidance: wait for Retry-After or a spent limit's reset, whichever is later, or else at
// least a minute, growing exponentially while the limit persists. Only a fallback wait for a
// query sent after the last pause lifted counts toward that growth, so the other queries of one
// burst do not compound it. A spent limit still refused after its reset, as when the local clock
// runs ahead, counts the same way.
func (g *GH) pause(sent time.Time, result reply) {
	g.mu.Lock()
	defer g.mu.Unlock()
	resume, signaled := spentUntil(result.rate)
	if asked := time.Now().Add(min(result.retryAfter, time.Hour)); result.retryAfter > 0 && asked.After(resume) {
		resume, signaled = asked, true
	}
	if signaled {
		g.holdUntil(resume)
		return
	}
	if !sent.Before(g.resumeAt) {
		g.strikes++
	}
	g.holdUntil(time.Now().Add(min(secondaryPause<<min(max(g.strikes-1, 0), 10), maxSecondaryPause)))
}

func spentUntil(rate github.Rate) (time.Time, bool) {
	if rate.Remaining != 0 || !time.Now().Before(rate.Reset.Time) {
		return time.Time{}, false
	}
	return time.Now().Add(min(time.Until(rate.Reset.Time)+resetBuffer, time.Hour)), true
}

// lifted records that a query sent at sent succeeded, so the next limit starts its waits over. A
// success that spent the primary limit holds every worker until its reset.
func (g *GH) lifted(sent time.Time, rate github.Rate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !sent.Before(g.resumeAt) {
		g.strikes = 0
	}
	if resume, spent := spentUntil(rate); spent {
		g.holdUntil(resume)
	}
}

// holdUntil moves resumeAt out to resume, with g.mu held. It warns when a pause starts, and again
// when a later reply pushes it out by a minute or more, so the announced time stays honest.
func (g *GH) holdUntil(resume time.Time) {
	if !resume.After(g.resumeAt) {
		return
	}
	announce := !time.Now().Before(g.resumeAt) || resume.Sub(g.resumeAt) >= time.Minute
	g.resumeAt = resume
	if announce && g.warn != nil {
		g.warn("GitHub's rate limit paused collection; resuming at %s", resume.Local().Format(time.TimeOnly))
	}
}

// retriedByTransport reports whether err is an HTTP error that gommon's transport has already
// retried or waited out, or one go-github refuses to send until a limit the transport gave up on
// lifts.
func retriedByTransport(err error) bool {
	var failed *github.ErrorResponse
	var abused *github.AbuseRateLimitError
	return errors.As(err, &failed) || errors.As(err, &abused)
}
