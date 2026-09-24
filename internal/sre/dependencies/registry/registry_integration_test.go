//go:build integration

package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Run with: go test -tags integration ./internal/sre/dependencies/...
func TestDockerHubLive(t *testing.T) {
	c := Client{}
	digest, err := c.Digest(context.Background(), "golang", "1.26.6-alpine3.24")
	if err != nil {
		t.Fatalf("golang:1.26.6-alpine3.24: %v", err)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest = %q, want sha256:…", digest)
	}

	if _, err := c.Digest(context.Background(), "golang", "1.26.6-alpine0.1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("golang:1.26.6-alpine0.1: err = %v, want ErrNotFound", err)
	}
}
