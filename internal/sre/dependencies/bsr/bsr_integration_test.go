//go:build integration

package bsr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Run with: go test -tags integration ./internal/sre/dependencies/...
func TestBufBuildLive(t *testing.T) {
	c := Client{}
	v, err := c.LatestPlugin(context.Background(), "buf.build/protocolbuffers/go")
	if err != nil {
		t.Fatalf("buf.build/protocolbuffers/go: %v", err)
	}
	if !strings.HasPrefix(v, "v1.") {
		t.Errorf("version = %q, want v1.…", v)
	}

	if _, err := c.LatestPlugin(context.Background(), "buf.build/project-init/no-such-plugin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing plugin: err = %v, want ErrNotFound", err)
	}
}
