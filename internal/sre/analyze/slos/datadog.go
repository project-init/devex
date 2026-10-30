package slos

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/project-init/gommon/pkg/datadog"
	gerror "github.com/project-init/gommon/pkg/errors"
)

type point = datadog.Point

// errTimeout is a query Datadog did not answer before the client timeout, or answered with a 504, so the batch can
// be split.
var errTimeout = gerror.ErrTimeout

// datadogClient asks Datadog through the gommon client, and turns its auth errors into what to fix.
type datadogClient struct {
	dd *datadog.Client
}

func newDatadogClient(site, apiKey, appKey string) (*datadogClient, error) {
	dd, err := datadog.NewClient(&http.Client{Timeout: time.Minute}, site, apiKey, appKey)
	if err != nil {
		return nil, err
	}
	return &datadogClient{dd: dd}, nil
}

func (c *datadogClient) query(ctx context.Context, queries []string, from, to time.Time) ([][]point, error) {
	points, err := c.dd.Query(ctx, queries, from, to)
	return points, explain(err)
}

// metricNames lists the metrics that reported since from, so a run can find the app that serves each RPC.
func (c *datadogClient) metricNames(ctx context.Context, from time.Time) ([]string, error) {
	names, err := c.dd.ListActiveMetrics(ctx, from)
	return names, explain(err)
}

func explain(err error) error {
	switch {
	case errors.Is(err, gerror.ErrForbidden):
		return errors.New("datadog refused the query (403): give the app key the timeseries_query scope, and check slos.datadog.site")
	case errors.Is(err, gerror.ErrPermissionDenied):
		return errors.New("datadog rejected the keys (401): check DD_API_KEY, DD_APP_KEY, and slos.datadog.site")
	}
	return err
}
