package slos

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/project-init/gommon/pkg/datadog"
	gerror "github.com/project-init/gommon/pkg/errors"
)

type point = datadog.Point

// datadogClient asks Datadog through the gommon client, and turns its auth errors into what to fix.
type datadogClient struct {
	dd *datadog.Client
}

// defaultSite is Datadog's US1 site, the one Datadog's own tools use when DD_SITE is not set.
const defaultSite = "datadoghq.com"

// datadogBaseURL is the API host of the org's Datadog site: --datadog-site, then DD_SITE, then DATADOG_SITE, then
// US1. The keys work only on their own site, so a wrong one fails on the first request with the 401 or 403 below.
func datadogBaseURL(site string, getenv func(string) string) string {
	return "https://api." + cmp.Or(site, getenv("DD_SITE"), getenv("DATADOG_SITE"), defaultSite)
}

func newDatadogClient(baseURL, apiKey, appKey string) *datadogClient {
	return &datadogClient{dd: datadog.NewClient(&http.Client{Timeout: time.Minute}, baseURL, apiKey, appKey)}
}

func (c *datadogClient) query(ctx context.Context, queries []string, from, to time.Time) ([][]point, error) {
	points, err := c.dd.Query(ctx, queries, from, to)
	return points, explain(err)
}

// explain turns Datadog's auth errors into what to fix.
func explain(err error) error {
	switch {
	case errors.Is(err, gerror.ErrForbidden):
		return errors.New("datadog refused the request (403): give the app key the timeseries_query scope, and check DD_SITE or --datadog-site")
	case errors.Is(err, gerror.ErrPermissionDenied):
		return errors.New("datadog rejected the keys (401): check DD_API_KEY, DD_APP_KEY, and DD_SITE or --datadog-site")
	}
	return err
}

type querier interface {
	query(ctx context.Context, queries []string, from, to time.Time) ([][]point, error)
}

// datadogSource is the metricSource for Datadog. It owns everything Datadog-shaped: each SLO's queries are Datadog
// queries, and the source adds the rollup that reads them one slice at a time.
type datadogSource struct {
	dd querier
}

// plan is what Datadog is asked for s, and how its answers, in the same order, become the series. An OpenSLO query is
// written without a rollup, so each one is read per slice.
func (d datadogSource) plan(s slo) ([]string, func([][]point) series) {
	secs := int(s.Slice.Seconds())
	ind := s.Indicator
	if s.Budgeting == occurrences {
		return []string{
			fmt.Sprintf("%s.as_count().rollup(sum, %d)", ind.Good, secs),
			fmt.Sprintf("%s.as_count().rollup(sum, %d)", ind.Total, secs),
		}, func(q [][]point) series { return series{Good: q[0], Total: q[1]} }
	}
	return []string{fmt.Sprintf("%s.rollup(avg, %d)", ind.Query, secs)},
		func(q [][]point) series { return series{Value: q[0]} }
}

func (d datadogSource) queries(s slo) []string {
	qs, _ := d.plan(s)
	return qs
}

// fetch asks for every SLO's queries in one request, and hands each SLO back its own answers.
func (d datadogSource) fetch(ctx context.Context, slos []slo, from, to time.Time) ([]series, error) {
	var qs []string
	decode := make([]func([][]point) series, len(slos))
	sizes := make([]int, len(slos))
	for i, s := range slos {
		q, dec := d.plan(s)
		qs, decode[i], sizes[i] = append(qs, q...), dec, len(q)
	}
	got, err := d.dd.query(ctx, qs, from, to)
	if err != nil {
		return nil, err
	}
	if len(got) != len(qs) {
		return nil, fmt.Errorf("datadog answered %d of %d queries", len(got), len(qs))
	}
	out := make([]series, len(slos))
	for i, n := range sizes {
		out[i], got = decode[i](got[:n]), got[n:]
	}
	return out, nil
}
