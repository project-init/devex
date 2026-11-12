package gh

import (
	"errors"
	"fmt"

	"github.com/google/go-github/v74/github"

	"github.com/project-init/gommon/pkg/githubclient"
)

type GH struct {
	ghClient     *github.Client
	organization string
}

func New(organization string) (*GH, error) {
	ghToken, err := githubclient.ResolveToken()
	if err != nil {
		return nil, githubAuthError(err)
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

func githubAuthError(err error) error {
	if errors.Is(err, githubclient.ErrAuthUnavailable) {
		return fmt.Errorf("GitHub auth required (run `gh auth login` or set GITHUB_TOKEN)")
	}
	return err
}
