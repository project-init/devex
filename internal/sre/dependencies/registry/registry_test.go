package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const publishedDigest = "sha256:1111"

// fakeRegistry publishes golang:1.26.9-alpine3.24 behind an anonymous Bearer challenge.
type fakeRegistry struct {
	srv          *httptest.Server
	challenge    string
	tokenStatus  int
	omitDigest   bool
	tokenQueries []string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{tokenStatus: http.StatusOK}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.challenge = fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:library/golang:pull"`, f.srv.URL)

	return f
}

func (f *fakeRegistry) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		f.tokenQueries = append(f.tokenQueries, r.URL.RawQuery)
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte(`{"token":"t0k"}`))

		return
	}
	if r.Header.Get("Authorization") != "Bearer t0k" {
		w.Header().Set("WWW-Authenticate", f.challenge)
		w.WriteHeader(http.StatusUnauthorized)

		return
	}
	if r.URL.Path != "/v2/library/golang/manifests/1.26.9-alpine3.24" {
		w.WriteHeader(http.StatusNotFound)

		return
	}
	if !f.omitDigest {
		w.Header().Set("Docker-Content-Digest", publishedDigest)
	}
	_, _ = w.Write([]byte("manifest"))
}

func (f *fakeRegistry) client() Client {
	return Client{HTTP: f.srv.Client()}
}

// image names the fake by host, so parseImage routes to it instead of Docker Hub.
func (f *fakeRegistry) image() string {
	return strings.TrimPrefix(f.srv.URL, "https://") + "/library/golang"
}

func TestDigestFollowsBearerChallenge(t *testing.T) {
	f := newFakeRegistry(t)
	digest, err := f.client().Digest(context.Background(), f.image(), "1.26.9-alpine3.24")
	if err != nil {
		t.Fatal(err)
	}
	if digest != publishedDigest {
		t.Errorf("digest = %q, want %q", digest, publishedDigest)
	}
	if len(f.tokenQueries) != 1 || !strings.Contains(f.tokenQueries[0], "service=fake") {
		t.Errorf("token queries = %q, want one carrying the challenge's service", f.tokenQueries)
	}
}

func TestDigestReportsMissingTag(t *testing.T) {
	f := newFakeRegistry(t)
	_, err := f.client().Digest(context.Background(), f.image(), "1.27.1-alpine3.24")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDigestReportsRefusedToken(t *testing.T) {
	f := newFakeRegistry(t)
	f.tokenStatus = http.StatusUnauthorized
	_, err := f.client().Digest(context.Background(), f.image(), "1.26.9-alpine3.24")
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("err = %v, want ErrAuthRequired", err)
	}
}

func TestDigestReportsBasicAuthAsCredentialsRequired(t *testing.T) {
	f := newFakeRegistry(t)
	f.challenge = `Basic realm="private"`
	_, err := f.client().Digest(context.Background(), f.image(), "1.26.9-alpine3.24")
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("err = %v, want ErrAuthRequired", err)
	}
}

func TestDigestRejectsPlainHTTPRealm(t *testing.T) {
	f := newFakeRegistry(t)
	f.challenge = `Bearer realm="http://attacker.example/token"`
	_, err := f.client().Digest(context.Background(), f.image(), "1.26.9-alpine3.24")
	if err == nil || errors.Is(err, ErrAuthRequired) || !strings.Contains(err.Error(), "not an https URL") {
		t.Errorf("err = %v, want an https realm error", err)
	}
}

func TestDigestHashesBodyWhenHeaderMissing(t *testing.T) {
	f := newFakeRegistry(t)
	f.omitDigest = true
	digest, err := f.client().Digest(context.Background(), f.image(), "1.26.9-alpine3.24")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("manifest"))
	if want := "sha256:" + hex.EncodeToString(sum[:]); digest != want {
		t.Errorf("digest = %q, want %q", digest, want)
	}
}

func TestParseImage(t *testing.T) {
	cases := []struct{ image, host, repo string }{
		{"golang", "registry-1.docker.io", "library/golang"},
		{"docker.io/library/golang", "registry-1.docker.io", "library/golang"},
		{"docker.io/golang", "registry-1.docker.io", "library/golang"},
		{"myorg/go", "registry-1.docker.io", "myorg/go"},
		{"public.ecr.aws/docker/library/golang", "public.ecr.aws", "docker/library/golang"},
		{"localhost:5000/golang", "localhost:5000", "golang"},
		{"cgr.dev/chainguard/go", "cgr.dev", "chainguard/go"},
	}
	for _, c := range cases {
		host, repo := parseImage(c.image)
		if host != c.host || repo != c.repo {
			t.Errorf("parseImage(%q) = %s, %s; want %s, %s", c.image, host, repo, c.host, c.repo)
		}
	}
}
