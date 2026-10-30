package slos

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	gerror "github.com/project-init/gommon/pkg/errors"
)

// TestExplainAuthErrors tells a 403 (the app key lacks timeseries_query) apart from a 401 (Datadog rejects the keys),
// and passes every other error through.
func TestExplainAuthErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{err: fmt.Errorf("Datadog returned HTTP 403: %w", gerror.ErrForbidden), want: "timeseries_query"},
		{err: fmt.Errorf("Datadog returned HTTP 401: %w", gerror.ErrPermissionDenied), want: "DD_API_KEY"},
		{err: fmt.Errorf("Datadog returned HTTP 400: %w", gerror.ErrBadRequest), want: "HTTP 400"},
	}
	for _, tc := range cases {
		if err := explain(tc.err); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("explain(%v) = %v, want one that says %q", tc.err, err, tc.want)
		}
	}
	if err := explain(nil); err != nil {
		t.Errorf("explain(nil) = %v", err)
	}
	if err := explain(fmt.Errorf("slow: %w", errTimeout)); !errors.Is(err, errTimeout) {
		t.Errorf("explain hid the timeout a batch splits on: %v", err)
	}
}
