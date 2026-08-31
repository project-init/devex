package gh

import (
	"errors"
	"os"

	"github.com/google/go-github/v74/github"

	"github.com/project-init/devex/internal/githubclient"
)

type GH struct {
	ghClient     *github.Client
	organization string
}

func New(organization string) (*GH, error) {
	ghToken := os.Getenv("GITHUB_TOKEN")
	if ghToken == "" {
		return nil, errors.New("GITHUB_TOKEN environment variable not set")
	}

	ghClient, err := githubclient.New(ghToken, "")
	if err != nil {
		return nil, err
	}

	return &GH{
		ghClient:     ghClient,
		organization: organization,
	}, nil
}
