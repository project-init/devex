package gh

import (
	"errors"
	"os"

	"github.com/google/go-github/v74/github"
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

	return &GH{
		ghClient:     github.NewClient(nil).WithAuthToken(ghToken),
		organization: organization,
	}, nil
}
