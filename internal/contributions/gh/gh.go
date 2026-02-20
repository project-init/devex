package gh

import (
	"os"

	"github.com/google/go-github/v74/github"
)

type GH struct {
	ghClient     *github.Client
	organization string
}

func New(organization string) *GH {
	return &GH{
		ghClient:     github.NewClient(nil).WithAuthToken(os.Getenv("GITHUB_TOKEN")),
		organization: organization,
	}
}
