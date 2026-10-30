package slos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	gerror "github.com/project-init/gommon/pkg/errors"
)

// TestDatadogReadsEachSLOPerSlice adds the rollup that reads the SLO's own query one slice at a time: the average for
// a threshold, and the counts summed for a ratio.
func TestDatadogReadsEachSLOPerSlice(t *testing.T) {
	threshold := slo{Budgeting: timeslices, Slice: 5 * time.Minute, Indicator: indicator{Query: "avg:a{env:prod}"}}
	ratio := slo{Budgeting: occurrences, Slice: time.Hour, Indicator: indicator{Good: "sum:g{*}", Total: "sum:t{*}"}}
	cases := []struct {
		s    slo
		want []string
	}{
		{s: threshold, want: []string{"avg:a{env:prod}.rollup(avg, 300)"}},
		{s: ratio, want: []string{"sum:g{*}.as_count().rollup(sum, 3600)", "sum:t{*}.as_count().rollup(sum, 3600)"}},
	}
	for _, tc := range cases {
		if got := (datadogSource{}).queries(tc.s); !slices.Equal(got, tc.want) {
			t.Errorf("queries = %v, want %v", got, tc.want)
		}
	}
}

// TestExplainAuthErrors tells a 403 (the app key lacks the scope) apart from a 401 (Datadog rejects the keys), and
// passes every other error through, including the timeout a batch splits on.
func TestExplainAuthErrors(t *testing.T) {
	// A wrong site fails on its first request with one of these, since keys work only on their own site.
	cases := []struct {
		err  error
		want []string
	}{
		{err: fmt.Errorf("HTTP 403: %w", gerror.ErrForbidden), want: []string{"timeseries_query", "DD_SITE"}},
		{err: fmt.Errorf("HTTP 401: %w", gerror.ErrPermissionDenied), want: []string{"DD_API_KEY", "DD_SITE"}},
		{err: fmt.Errorf("HTTP 400: %w", gerror.ErrBadRequest), want: []string{"HTTP 400"}},
	}
	for _, tc := range cases {
		err := explain(tc.err)
		for _, want := range tc.want {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("explain(%v) = %v, want one that says %q", tc.err, err, want)
			}
		}
	}
	if err := explain(nil); err != nil {
		t.Errorf("explain(nil) = %v", err)
	}
	if err := explain(fmt.Errorf("slow: %w", errTimeout)); !errors.Is(err, errTimeout) {
		t.Errorf("explain hid the timeout a batch splits on: %v", err)
	}
}

// TestA403NamesTheScope sends a 403 back through the real client, so the error names the scope the query needs.
func TestA403NamesTheScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	c := newDatadogClient(server.URL, "api", "app")
	if _, err := c.query(context.Background(), []string{"avg:a{*}"}, time.Now(), time.Now()); err == nil || !strings.Contains(err.Error(), "timeseries_query") {
		t.Errorf("error = %v, want one that names timeseries_query", err)
	}
}
