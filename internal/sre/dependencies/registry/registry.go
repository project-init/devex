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

// Client resolves image tags against their registries.
type Client struct {
	// HTTP is the client used for every request. Nil uses http.DefaultClient.
	HTTP *http.Client
}

// Digest returns the manifest digest of image:tag. It returns ErrNotFound when the registry
// does not publish the tag, and ErrAuthRequired when it refuses anonymous access.
func (c Client) Digest(ctx context.Context, image, tag string) (string, error) {
	host, repo := parseImage(image)
	manifest := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, repo, url.PathEscape(tag))

	var token string
	resp, err := c.head(ctx, manifest, token)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if token, err = c.token(ctx, resp.Header.Get("WWW-Authenticate"), repo); err != nil {
			return "", err
		}
		if resp, err = c.head(ctx, manifest, token); err != nil {
			return "", err
		}
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
	if resp.StatusCode != http.StatusOK {
		return "", ErrAuthRequired
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
