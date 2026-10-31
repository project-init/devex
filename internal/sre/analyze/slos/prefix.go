package slos

import (
	"context"
	"slices"
	"strings"
	"time"
)

// prefixWindow is how far back the metric list looks for a metric that reported. A metric that has not reported in
// this long has no data to score either, so the RPC reads NO DATA whichever way it is found.
const prefixWindow = 30 * 24 * time.Hour

// p95Suffix ends the one metric every scored RPC must have, so the metric list names the app that serves it.
const p95Suffix = ".response_time.95percentile"

// resolvePrefixes fills in each RPC's metric prefix from the names Datadog has reported, because the app that
// serves an RPC is a deploy, not something the protos know: entities RPCs report under users, and one group's RPCs
// can come from several apps. An RPC that two apps report is returned once per app. An RPC that no app reports
// keeps an empty prefix, and the report reads it as NO DATA.
func resolvePrefixes(eps []endpoint, names []string) []endpoint {
	// Every prefix that reported each RPC's metric, e.g. acmeOrdersV1OrdersServiceGetOrder -> [acme].
	byMetric := map[string][]string{}
	for _, name := range names {
		body, ok := strings.CutSuffix(name, p95Suffix)
		if !ok {
			continue
		}
		prefix, metric, ok := strings.Cut(body, ".")
		if !ok || prefix == "" || metric == "" {
			continue
		}
		byMetric[metric] = append(byMetric[metric], prefix)
	}

	out := make([]endpoint, 0, len(eps))
	for _, ep := range eps {
		prefixes := byMetric[ep.Metric]
		if len(prefixes) == 0 {
			out = append(out, ep)
			continue
		}
		for _, prefix := range slices.Sorted(slices.Values(prefixes)) {
			found := ep
			found.Prefix = prefix
			out = append(out, found)
		}
	}
	return out
}

// pinPrefixes puts prefix on every RPC, for a --metric-prefix run that skips the metric list.
func pinPrefixes(eps []endpoint, prefix string) []endpoint {
	out := make([]endpoint, len(eps))
	for i, ep := range eps {
		ep.Prefix = prefix
		out[i] = ep
	}
	return out
}

// prefixesOf names the apps the RPCs report under, for the report header.
func prefixesOf(eps []endpoint) []string {
	var found []string
	for _, ep := range eps {
		if ep.Prefix != "" && !slices.Contains(found, ep.Prefix) {
			found = append(found, ep.Prefix)
		}
	}
	slices.Sort(found)
	return found
}

// lister is the metric list resolvePrefixes reads.
type lister interface {
	metricNames(ctx context.Context, from time.Time) ([]string, error)
}
