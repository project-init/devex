package githubclient

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/go-github/v74/github"
)

func New(token string, baseURL string) (*github.Client, error) {
	client := github.NewClient(nil)

	if token != "" {
		client = client.WithAuthToken(token)
	}

	if baseURL != "" {
		parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/") + "/")
		if err != nil {
			return nil, fmt.Errorf("parse github base url: %w", err)
		}
		client.BaseURL = parsed
	}

	return client, nil
}
