package gh

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

var invalidLogins = []string{
	"github-actions[bot]",
}

func (g *GH) GetRepoPRs(ctx context.Context, cutoff time.Time, repo string, cfg *config.Config) ([]types.PR, error) {
	prs, err := g.getAllPrs(ctx, repo, cutoff)
	if err != nil {
		prs, err = g.getAllPrs(ctx, repo, cutoff)
		if err != nil {
			log.Fatalf("failed to get all prs: %s", err.Error())
		}
	}

	return prs, err
}

func (g *GH) getAllPrs(ctx context.Context, repoName string, cutoff time.Time) ([]types.PR, error) {
	page := 1
	prs, err := g.getRepoPrs(ctx, repoName, page, cutoff)
	if err != nil {
		return nil, err
	}

	foundPrs := make([]types.PR, 0)
	for len(prs) > 0 {
		foundPrs = append(foundPrs, prs...)
		page++
		prs, err = g.getRepoPrs(ctx, repoName, page, cutoff)
		if err != nil {
			return nil, err
		}
	}
	return foundPrs, nil
}

func (g *GH) getRepoPrs(ctx context.Context, repoName string, page int, cutoff time.Time) ([]types.PR, error) {
	prs, _, err := g.ghClient.PullRequests.List(ctx, g.organization, repoName, &github.PullRequestListOptions{
		State:     "closed",
		Head:      "",
		Base:      "",
		Sort:      "created",
		Direction: "desc",
		ListOptions: github.ListOptions{
			Page:    page,
			PerPage: 100,
		},
	})
	if err != nil {
		return nil, err
	}

	foundPrs := make([]types.PR, 0)
	for _, pr := range prs {
		// Not Merged, skip
		if pr.MergedAt == nil {
			continue
		}

		if pr.MergedAt.GetTime().Before(cutoff) || pr.MergedAt.GetTime().Equal(cutoff) {
			continue
		}

		if slices.Contains(invalidLogins, pr.GetUser().GetLogin()) {
			continue
		}

		reviews, _, err := g.ghClient.PullRequests.ListReviews(ctx, g.organization, repoName, pr.GetNumber(), &github.ListOptions{
			Page:    0,
			PerPage: 15,
		})
		if err != nil {
			return nil, err
		}
		reviewData := make([]string, len(reviews))
		for index, review := range reviews {
			reviewData[index] = fmt.Sprintf("%s:%s", review.GetUser().GetLogin(), review.GetState())
		}
		timeToMerge := pr.GetMergedAt().Sub(pr.GetCreatedAt().Time)

		prMeta := types.PR{
			Author:      pr.GetUser().GetLogin(),
			TimeToMerge: timeToMerge,
			MergedAt:    pr.GetMergedAt().Time,
			Number:      pr.GetNumber(),
			Repo:        repoName,
			Reviews:     strings.Join(reviewData, "!"),
		}
		foundPrs = append(foundPrs, prMeta)
	}
	return foundPrs, nil
}
