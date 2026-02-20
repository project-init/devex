package gh

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/contributions/config"
)

func (g *GH) GetRepos(ctx context.Context, cfg *config.Config) ([]string, error) {
	if len(cfg.ReposToCheck) > 0 {
		return cfg.ReposToCheck, nil
	}

	page := 1
	repos, err := g.getRepos(ctx, page)
	if err != nil {
		return nil, err
	}
	foundRepos := make([]string, 0)
	for len(repos) > 0 {
		for _, repo := range repos {
			if slices.Contains(cfg.ReposToSkip, repo) {
				continue
			}

			if cfg.LastRepo == nil || strings.Compare(repo, *cfg.LastRepo) == 1 {
				foundRepos = append(foundRepos, repo)
			}
		}
		page++
		repos, err = g.getRepos(ctx, page)
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(foundRepos)
	return foundRepos, nil
}

func (g *GH) getRepos(ctx context.Context, page int) ([]string, error) {
	opt := &github.RepositoryListByOrgOptions{
		Type: "member",
		ListOptions: github.ListOptions{
			Page:    page,
			PerPage: 100,
		},
	}
	repos, _, err := g.ghClient.Repositories.ListByOrg(
		context.WithValue(ctx, github.SleepUntilPrimaryRateLimitResetWhenRateLimited, true),
		g.organization,
		opt,
	)
	if err != nil {
		return nil, err
	}
	repoNames := make([]string, len(repos))
	for index, repo := range repos {
		repoNames[index] = repo.GetName()
	}
	return repoNames, nil
}
