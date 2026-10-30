package slos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

type querier interface {
	query(ctx context.Context, queries []string, from, to time.Time) ([][]point, error)
}

// client is everything a run asks Datadog for: the metric list that names the app serving each RPC, and the
// queries that score it.
type client interface {
	querier
	lister
}

// The queries asked for each RPC, in this order.
const (
	qP95 = iota
	qMedian
	qMax
	qCount
	qFailed
	queriesPerEndpoint
)

func endpointQueries(ep endpoint) [queriesPerEndpoint]string {
	secs := int(ep.Rollup.Seconds())
	scope := "{" + ep.Scope + "}"
	metric := ep.MetricName()
	return [queriesPerEndpoint]string{
		qP95:    fmt.Sprintf("avg:%s.response_time.95percentile%s.rollup(avg, %d)", metric, scope, secs),
		qMedian: fmt.Sprintf("avg:%s.response_time.median%s.rollup(avg, %d)", metric, scope, secs),
		qMax:    fmt.Sprintf("max:%s.response_time.max%s.rollup(max, %d)", metric, scope, secs),
		qCount:  fmt.Sprintf("sum:%s.response_time.count%s.as_count().rollup(sum, %d)", metric, scope, secs),
		qFailed: fmt.Sprintf("sum:%s.failed%s.as_count().rollup(sum, %d)", metric, scope, secs),
	}
}

// progress writes a running count. On a terminal it redraws one line; elsewhere it writes the final count only.
type progress struct {
	w    io.Writer
	live bool
}

func (p progress) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, format, args...)
}

// fetchAndScore scores every RPC, batch RPCs per Datadog request, in eps order.
func fetchAndScore(ctx context.Context, dd querier, eps []endpoint, from, to time.Time, batch int, p progress) ([]result, error) {
	// An RPC no app reports has no metric to ask for. It reads NO DATA without a query, so a group where most RPCs
	// are not instrumented still costs one request per instrumented RPC.
	var results []result
	var query []endpoint
	for _, ep := range eps {
		if ep.Prefix == "" {
			results = append(results, result{Endpoint: ep, Verdict: noData})
			continue
		}
		query = append(query, ep)
	}
	eps = query
	if p.live {
		p.printf("Fetching endpoints... 0/%d", len(eps))
	}
	for start := 0; start < len(eps); start += batch {
		chunk := eps[start:min(start+batch, len(eps))]
		got, err := queryBatch(ctx, dd, chunk, from, to, p)
		if err != nil {
			if p.live && start > 0 {
				p.printf("\n")
			}
			return nil, err
		}
		results = append(results, got...)
		if p.live {
			p.printf("\rFetching endpoints... %d/%d", start+len(chunk), len(eps))
		}
	}
	if !p.live {
		p.printf("Fetching endpoints... %d/%d", len(eps), len(eps))
	}
	p.printf("\n\n")
	return results, nil
}

// queryBatch asks for chunk's queries in one call and scores each RPC. A long window over many RPCs can take Datadog
// longer than the client waits, so a batch that times out is split in half and asked again, down to one RPC.
func queryBatch(ctx context.Context, dd querier, chunk []endpoint, from, to time.Time, p progress) ([]result, error) {
	var qs []string
	for _, ep := range chunk {
		q := endpointQueries(ep)
		qs = append(qs, q[:]...)
	}
	got, err := dd.query(ctx, qs, from, to)
	if errors.Is(err, errTimeout) && len(chunk) > 1 {
		half := len(chunk) / 2
		p.printf("\n%s..%s: %v; asking again %d RPCs at a time\n", chunk[0].Name(), chunk[len(chunk)-1].Name(), err, len(chunk)-half)
		first, err := queryBatch(ctx, dd, chunk[:half], from, to, p)
		if err != nil {
			return nil, err
		}
		rest, err := queryBatch(ctx, dd, chunk[half:], from, to, p)
		return append(first, rest...), err
	}
	if err != nil {
		return nil, fmt.Errorf("query %s..%s: %w", chunk[0].Name(), chunk[len(chunk)-1].Name(), err)
	}
	out := make([]result, 0, len(chunk))
	for i, ep := range chunk {
		q := got[i*queriesPerEndpoint : (i+1)*queriesPerEndpoint]
		out = append(out, score(ep, q[qP95], q[qMedian], q[qMax], q[qCount], q[qFailed]))
	}
	return out, nil
}
