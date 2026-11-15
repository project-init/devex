package registry

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const publishedDigest = "sha256:1111"

// fakeRegistry publishes golang:1.26.9-alpine3.24 behind an anonymous Bearer challenge.
type fakeRegistry struct {
	srv          *httptest.Server
	challenge    string
	tokenStatus  int
	omitDigest   bool
	tokenQueries []string

	mu       sync.Mutex
	requests int
	token    string // the token the server accepts; empty means t0k
	private  bool   // the manifest refuses every token, as Docker Hub does a private image
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
	f.mu.Lock()
	f.requests++
	token := cmp.Or(f.token, "t0k")
	if r.URL.Path == "/token" {
		f.tokenQueries = append(f.tokenQueries, r.URL.RawQuery)
	}
	f.mu.Unlock()
	if r.URL.Path == "/token" {
		w.WriteHeader(f.tokenStatus)
		_, _ = fmt.Fprintf(w, `{"token":%q}`, token)

		return
	}
	if f.private || r.Header.Get("Authorization") != "Bearer "+token {
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

func TestCachedTokenServesEveryTagOfARepository(t *testing.T) {
	f := newFakeRegistry(t)
	client := NewClient(f.srv.Client())
	for _, tag := range []string{"1.26.9-alpine3.24", "a", "b", "c", "d"} {
		_, _ = client.Digest(context.Background(), f.image(), tag)
	}
	// The first tag costs a refused HEAD, a token, and a HEAD; every later tag one HEAD.
	if f.requests != 7 {
		t.Errorf("requests = %d, want 7 for five tags of one repository", f.requests)
	}
}

func TestConcurrentLookupsShareOneTokenFetch(t *testing.T) {
	f := newFakeRegistry(t)
	client := NewClient(f.srv.Client())
	var wg sync.WaitGroup
	for _, tag := range []string{"1.26.9-alpine3.24", "a", "b", "c", "d"} {
		wg.Go(func() { _, _ = client.Digest(context.Background(), f.image(), tag) })
	}
	wg.Wait()
	if len(f.tokenQueries) != 1 || f.requests != 7 {
		t.Errorf("token fetches = %d, requests = %d; want 1 and 7", len(f.tokenQueries), f.requests)
	}
}

func TestExpiredTokenIsRenewedOnce(t *testing.T) {
	f := newFakeRegistry(t)
	client := NewClient(f.srv.Client())
	if _, err := client.Digest(context.Background(), f.image(), "1.26.9-alpine3.24"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.token = "fresh" // the cached t0k has expired
	f.mu.Unlock()
	digest, err := client.Digest(context.Background(), f.image(), "1.26.9-alpine3.24")
	if err != nil || digest != publishedDigest || len(f.tokenQueries) != 2 {
		t.Errorf("Digest() = %q, %v after %d token fetches; want the digest through one renewal", digest, err, len(f.tokenQueries))
	}
}

func TestConcurrentRenewalsShareOneFetch(t *testing.T) {
	f := newFakeRegistry(t)
	client := NewClient(f.srv.Client())
	if _, err := client.Digest(context.Background(), f.image(), "1.26.9-alpine3.24"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.token = "fresh" // every lookup below holds the expired t0k
	f.mu.Unlock()
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { _, _ = client.Digest(context.Background(), f.image(), "1.26.9-alpine3.24") })
	}
	wg.Wait()
	if len(f.tokenQueries) != 2 {
		t.Errorf("token fetches = %d, want 2: the first, then one renewal shared by all five", len(f.tokenQueries))
	}
}

func TestAnonymousRepositoryLookupsRunConcurrently(t *testing.T) {
	// The registry needs no token, and answers the probe at once but every later request only
	// once a second request is in flight beside it.
	arrived := make(chan struct{}, 2)
	var probed sync.Once
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := false
		probed.Do(func() { first = true })
		if !first {
			arrived <- struct{}{}
			deadline := time.After(2 * time.Second)
			for len(arrived) < 2 {
				select {
				case <-deadline:
					w.WriteHeader(http.StatusInternalServerError)
					return
				case <-time.After(time.Millisecond):
				}
			}
		}
		w.Header().Set("Docker-Content-Digest", publishedDigest)
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.Client())
	image := strings.TrimPrefix(srv.URL, "https://") + "/library/golang"
	if _, err := client.Digest(context.Background(), image, "probe"); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for _, tag := range []string{"a", "b"} {
		go func() { _, err := client.Digest(context.Background(), image, tag); errs <- err }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("Digest() = %v, want both lookups in flight at once", err)
		}
	}
}

func TestDeniedRepositorySettlesAfterOneTokenRequest(t *testing.T) {
	f := newFakeRegistry(t)
	f.tokenStatus = http.StatusUnauthorized // a private repository, with no credentials
	client := NewClient(f.srv.Client())
	var wg sync.WaitGroup
	for _, tag := range []string{"a", "b", "c", "d", "e"} {
		wg.Go(func() {
			if _, err := client.Digest(context.Background(), f.image(), tag); !errors.Is(err, ErrAuthRequired) {
				t.Errorf("Digest(%s) = %v, want ErrAuthRequired", tag, err)
			}
		})
	}
	wg.Wait()
	if len(f.tokenQueries) != 1 {
		t.Errorf("token requests = %d, want 1: the refusal holds for the run", len(f.tokenQueries))
	}
}

func TestUnavailableTokenEndpointIsRetried(t *testing.T) {
	f := newFakeRegistry(t)
	f.tokenStatus = http.StatusServiceUnavailable
	client := NewClient(f.srv.Client())
	for range 2 {
		if _, err := client.Digest(context.Background(), f.image(), "1.26.9-alpine3.24"); err == nil || errors.Is(err, ErrAuthRequired) {
			t.Errorf("Digest() = %v, want the outage, not a credentials refusal", err)
		}
	}
	if len(f.tokenQueries) != 2 {
		t.Errorf("token requests = %d, want 2: a 503 is not a refusal to remember", len(f.tokenQueries))
	}
}

func TestRefusedRepositorySettlesAfterOneToken(t *testing.T) {
	f := newFakeRegistry(t)
	f.private = true
	client := NewClient(f.srv.Client())
	for _, tag := range []string{"a", "b", "c"} {
		if _, err := client.Digest(context.Background(), f.image(), tag); !errors.Is(err, ErrAuthRequired) {
			t.Errorf("Digest(%s) = %v, want ErrAuthRequired", tag, err)
		}
	}
	// The first tag: a refused HEAD, a token, a HEAD the token cannot open; later tags one HEAD.
	if len(f.tokenQueries) != 1 || f.requests != 5 {
		t.Errorf("token requests = %d, requests = %d; want 1 and 5", len(f.tokenQueries), f.requests)
	}
}
