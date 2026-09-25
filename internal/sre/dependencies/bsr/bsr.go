// Package bsr looks up remote plugin versions on a Buf Schema Registry, using anonymous access
// to its Connect API.
package bsr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotFound reports a plugin the registry does not publish.
var ErrNotFound = errors.New("plugin not published")

const (
	latestPluginPath = "/buf.alpha.registry.v1alpha1.PluginCurationService/GetLatestCuratedPlugin"
	maxResponseBytes = 1 << 20
)

// Client resolves remote plugin versions.
type Client struct {
	// HTTP is the client used for every request. Nil uses http.DefaultClient.
	HTTP *http.Client
	// BaseURL replaces https://<registry host> in every request. Tests set it.
	BaseURL string
}

// LatestPlugin returns the newest version of plugin, a remote plugin reference without a
// version, such as "buf.build/apple/swift".
func (c Client) LatestPlugin(ctx context.Context, plugin string) (string, error) {
	parts := strings.Split(plugin, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("remote plugin %q: want <registry>/<owner>/<name>", plugin)
	}
	base := c.BaseURL
	if base == "" {
		base = "https://" + parts[0]
	}
	body, err := json.Marshal(map[string]string{"owner": parts[1], "name": parts[2]})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+latestPluginPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("remote plugin %s: %w", plugin, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var reply struct {
		Plugin struct {
			Version string `json:"version"`
		} `json:"plugin"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&reply); err != nil {
		return "", fmt.Errorf("remote plugin %s: %s: %w", plugin, resp.Status, err)
	}
	switch {
	case reply.Code == "not_found":
		return "", fmt.Errorf("remote plugin %s: %w", plugin, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("remote plugin %s: %s: %s", plugin, resp.Status, reply.Message)
	case reply.Plugin.Version == "":
		return "", fmt.Errorf("remote plugin %s: the registry returned no version", plugin)
	}

	return reply.Plugin.Version, nil
}
