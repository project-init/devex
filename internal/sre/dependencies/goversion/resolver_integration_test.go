//go:build integration

package goversion

import (
	"context"
	"testing"
)

// Run with: go test -tags integration ./internal/sre/dependencies/...
func TestGoDevLive(t *testing.T) {
	releases, err := Resolver{}.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go120, _ := Parse("1.20.0")
	if !Released(releases, go120) {
		t.Error("go.dev omits go1.20; include=all should list retired minor lines")
	}
	latest, err := Latest(releases, go120)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Compare(go120) <= 0 {
		t.Errorf("Latest = %s, want newer than 1.20.0", latest)
	}
}
