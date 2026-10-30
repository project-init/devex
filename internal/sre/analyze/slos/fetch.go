package slos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	gerror "github.com/project-init/gommon/pkg/errors"
)

// errTimeout is what a source returns when the backend did not answer in time, so the batch can be split. It is the
// shared gommon sentinel, so a source wraps the client's own timeout error and the core needs no backend's errors.
var errTimeout = gerror.ErrTimeout

// metricSource is one backend that holds the data an SLO is scored on. Each source owns its query syntax and hands
// back the points per slice, so the scoring does its math without knowing which backend it talks to. Datadog is the
// first; another (Prometheus, say) is one more implementation.
type metricSource interface {
	// queries are what fetch asks the backend for s, for --dry-run.
	queries(s slo) []string
	// fetch reads each SLO's series from from to to, in order, in as few requests as the backend allows. It returns
	// errTimeout when the backend did not answer in time, so a batch can be split and asked again.
	fetch(ctx context.Context, slos []slo, from, to time.Time) ([]series, error)
}

// series is what a source reads for one SLO, per slice: Value for a threshold, Good and Total for a ratio.
type series struct {
	Value       []point
	Good, Total []point
}

// progress writes a running count. On a terminal it redraws one line; elsewhere it writes the final count only.
type progress struct {
	w    io.Writer
	live bool
}

func (p progress) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, format, args...)
}

// fetchAndScore scores every SLO over its own window, ending at. A request carries SLOs of one window only, since it
// asks for one range, and at most batch of them.
func fetchAndScore(ctx context.Context, src metricSource, slos []slo, at time.Time, batch int, minEvents float64, p progress) ([]result, error) {
	var windows []time.Duration
	for _, s := range slos {
		if !slices.Contains(windows, s.Window) {
			windows = append(windows, s.Window)
		}
	}
	if p.live {
		p.printf("Fetching SLOs... 0/%d", len(slos))
	}
	var results []result
	for _, w := range windows {
		group := slices.DeleteFunc(slices.Clone(slos), func(s slo) bool { return s.Window != w })
		for start := 0; start < len(group); start += batch {
			chunk := group[start:min(start+batch, len(group))]
			got, err := queryBatch(ctx, src, chunk, at.Add(-w), at, minEvents, p)
			if err != nil {
				if p.live && len(results) > 0 {
					p.printf("\n")
				}
				return nil, err
			}
			results = append(results, got...)
			if p.live {
				p.printf("\rFetching SLOs... %d/%d", len(results), len(slos))
			}
		}
	}
	if !p.live {
		p.printf("Fetching SLOs... %d/%d", len(slos), len(slos))
	}
	p.printf("\n\n")
	return results, nil
}

// queryBatch reads chunk in one call and scores each SLO. A long window over many SLOs can take the backend longer
// than the client waits, so a batch that times out is split in half and asked again, down to one SLO.
func queryBatch(ctx context.Context, src metricSource, chunk []slo, from, to time.Time, minEvents float64, p progress) ([]result, error) {
	got, err := src.fetch(ctx, chunk, from, to)
	if errors.Is(err, errTimeout) && len(chunk) > 1 {
		half := len(chunk) / 2
		p.printf("\n%s..%s: %v; asking again %d SLOs at a time\n", chunk[0].Name(), chunk[len(chunk)-1].Name(), err, len(chunk)-half)
		first, err := queryBatch(ctx, src, chunk[:half], from, to, minEvents, p)
		if err != nil {
			return nil, err
		}
		rest, err := queryBatch(ctx, src, chunk[half:], from, to, minEvents, p)
		return append(first, rest...), err
	}
	if err != nil {
		return nil, fmt.Errorf("query %s..%s: %w", chunk[0].Name(), chunk[len(chunk)-1].Name(), err)
	}
	out := make([]result, 0, len(chunk))
	for i, s := range chunk {
		out = append(out, score(s, got[i], minEvents))
	}
	return out, nil
}
