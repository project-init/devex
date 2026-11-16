// Package registry checks that container image tags exist and resolves their digests using
// anonymous access to the OCI distribution API.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

var (
	// ErrNotFound reports an unpublished tag.
	ErrNotFound = errors.New("tag not published")
	// ErrAuthRequired reports a registry that refuses anonymous pulls.
	ErrAuthRequired = errors.New("registry requires credentials")
)

const (
	dockerHubHost    = "registry-1.docker.io"
	maxTokenBytes    = 1 << 20
	maxManifestBytes = 4 << 20
)

// manifestTypes covers multi-platform indexes first, so a digest pin resolves to the same
// index digest `docker pull` records.
var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// Client resolves image tags against their registries. It is safe for concurrent use.
type Client struct {
	// HTTP is the client used for every request. Nil uses http.DefaultClient.
	HTTP *http.Client
	// tokens holds a pull token per repository; nil fetches one for every tag.
	tokens *tokenCache
}

// NewClient returns a Client that reuses each repository's pull token across tags, so checking
// five tags of one image costs 7 requests rather than 15.
func NewClient(httpClient *http.Client) Client {
	return Client{HTTP: httpClient, tokens: &tokenCache{entries: map[string]*tokenEntry{}}}
}

// Digest returns the manifest digest of image:tag. It returns ErrNotFound when the registry
// does not publish the tag, and ErrAuthRequired when it refuses anonymous access.
func (c Client) Digest(ctx context.Context, image, tag string) (string, error) {
	host, repo := parseImage(image)
	manifest := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, repo, url.PathEscape(tag))

	resp, token, err := c.authorizedHead(ctx, manifest, host+"/"+repo, repo)
	if err != nil {
		return "", err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if digest := resp.Header.Get("Docker-Content-Digest"); digest != "" {
			return digest, nil
		}

		return c.digestFromBody(ctx, manifest, token)
	case http.StatusNotFound:
		return "", ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrAuthRequired
	default:
		return "", fmt.Errorf("%s:%s: registry returned %s", image, tag, resp.Status)
	}
}

// authorizedHead requests manifest with the repository's cached token, fetching or renewing the
// token when the registry refuses it. The first lookup in a repository holds the repository's
// entry until it learns whether the registry wants a token, so concurrent lookups share its one
// token fetch instead of each making their own.
func (c Client) authorizedHead(ctx context.Context, manifest, key, repo string) (*http.Response, string, error) {
	e := c.tokens.entry(key)
	e.mu.Lock()
	first, token := !e.probed, e.token
	if !first {
		e.mu.Unlock()
	}
	resp, err := c.head(ctx, manifest, token)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		if first {
			e.probed = err == nil // the registry serves this repository without a token
			e.mu.Unlock()
		}
		return resp, token, err
	}

	if !first {
		e.mu.Lock()
	}
	if first || e.token == token {
		// Nobody renewed the refused token while this lookup waited, so fetch one, unless the
		// registry already refused this repository anonymous access.
		if e.denied {
			err = ErrAuthRequired
		} else if token, err = c.token(ctx, resp.Header.Get("WWW-Authenticate"), repo); err == nil {
			e.token = token
		}
		// A refusal holds for the rest of the run; an outage or a network error leaves the next
		// lookup to retry.
		if err == nil || errors.Is(err, ErrAuthRequired) {
			e.probed, e.denied = true, err != nil
		}
	} else {
		token = e.token
	}
	e.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	resp, err = c.head(ctx, manifest, token)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		// The registry grants anonymous tokens but refuses this repository with one, as Docker
		// Hub does for a private image, so later lookups need not fetch another.
		e.mu.Lock()
		if e.token == token {
			e.denied = true
		}
		e.mu.Unlock()
	}
	return resp, token, err
}

func (c Client) head(ctx context.Context, manifest, token string) (*http.Response, error) {
	resp, err := c.request(ctx, http.MethodHead, manifest, token)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()

	return resp, nil
}

// digestFromBody hashes the manifest for registries that omit Docker-Content-Digest on HEAD.
func (c Client) digestFromBody(ctx context.Context, manifest, token string) (string, error) {
	resp, err := c.request(ctx, http.MethodGet, manifest, token)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch manifest: registry returned %s", resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(resp.Body, maxManifestBytes)); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// token fetches an anonymous pull token from the realm a Bearer challenge names.
func (c Client) token(ctx context.Context, challenge, repo string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", ErrAuthRequired
	}
	values := map[string]string{}
	for _, m := range challengeParam.FindAllStringSubmatch(params, -1) {
		values[strings.ToLower(m[1])] = m[2]
	}
	realm, err := url.Parse(values["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" {
		return "", fmt.Errorf("registry token realm %q is not an https URL", values["realm"])
	}
	scope := values["scope"]
	if scope == "" {
		scope = "repository:" + repo + ":pull"
	}
	q := realm.Query()
	q.Set("scope", scope)
	if service := values["service"]; service != "" {
		q.Set("service", service)
	}
	realm.RawQuery = q.Encode()

	resp, err := c.request(ctx, http.MethodGet, realm.String(), "")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrAuthRequired
	default:
		// An outage, such as a 503, fails like a manifest request would, rather than as a refusal.
		return "", fmt.Errorf("fetch registry token: registry returned %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTokenBytes)).Decode(&body); err != nil {
		return "", fmt.Errorf("decode registry token: %w", err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}

	return "", ErrAuthRequired
}

func (c Client) request(ctx context.Context, method, target, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestTypes)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query registry: %w", err)
	}

	return resp, nil
}

// parseImage splits an image name into its registry host and repository path, applying
// Docker Hub's defaults: "golang" becomes registry-1.docker.io and library/golang.
func parseImage(image string) (host, repo string) {
	first, rest, found := strings.Cut(image, "/")
	if !found || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		host, repo = dockerHubHost, image
	} else {
		host, repo = first, rest
	}
	if host == "docker.io" || host == "index.docker.io" {
		host = dockerHubHost
	}
	if host == dockerHubHost && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}

	return host, repo
}

// tokenCache holds one anonymous pull token per registry host and repository.
type tokenCache struct {
	mu      sync.Mutex
	entries map[string]*tokenEntry
}

// entry returns key's entry. A nil cache, as in a zero Client, returns a fresh entry each time,
// so every lookup fetches its own token.
func (c *tokenCache) entry(key string) *tokenEntry {
	if c == nil {
		return &tokenEntry{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		e = &tokenEntry{}
		c.entries[key] = e
	}
	return e
}

type tokenEntry struct {
	mu     sync.Mutex
	token  string
	probed bool // a lookup learned whether the registry wants a token, and fetched it if so
	denied bool // the registry refused this repository a token
}
