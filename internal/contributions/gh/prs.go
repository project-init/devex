package gh

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/config"
)

type PR struct {
	Author      string        `csv:"author"`
	TimeToMerge time.Duration `csv:"time_to_merge_duration"`
	Repo        string        `csv:"repo"`
	Number      int           `csv:"number"`
	Reviews     string        `csv:"reviews"`
}

var invalidLogins = []string{
	"github-actions[bot]",
}

func (g *GH) GetPRs(ctx context.Context, cutoff time.Time, repos []string, cfg *config.Config) []PR {
	allPrs := make([]PR, 0)
	for _, repo := range repos {
		prs, err := g.getAllPrs(ctx, repo, cutoff)
		if err != nil {
			prs, err = g.getAllPrs(ctx, repo, cutoff)
			if err != nil {
				log.Fatalf("failed to get all prs: %s", err.Error())
			}
		}
		allPrs = append(allPrs, prs...)
		log.Printf("collected %d PRs from %s\n", len(prs), repo)
	}

	if err := prsToCSV(allPrs, cfg); err != nil {
		log.Fatal(err)
	}

	return allPrs
}

func prsToCSV(prs []PR, cfg *config.Config) error {
	year, month, day := time.Now().Date()
	f, err := os.Create(fmt.Sprintf("%s/%d_%02d_%02d_last_%d_days_prs.csv", cfg.OutputDirectories.Prs, year, month, day, cfg.NumLookBackDays))
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	if err != nil {
		return err
	}

	bytes, err := csvutil.Marshal(prs)
	if err != nil {
		return err
	}

	_, err = f.Write(bytes)
	if err != nil {
		return err
	}

	err = f.Close()
	if err != nil {
		return err
	}

	return nil
}

func (g *GH) getAllPrs(ctx context.Context, repoName string, cutoff time.Time) ([]PR, error) {
	page := 1
	prs, err := g.getRepoPrs(ctx, repoName, page, cutoff)
	if err != nil {
		return nil, err
	}

	foundPrs := make([]PR, 0)
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

func (g *GH) getRepoPrs(ctx context.Context, repoName string, page int, cutoff time.Time) ([]PR, error) {
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

	foundPrs := make([]PR, 0)
	for _, pr := range prs {
		// Not Merged, skip
		if pr.MergedAt == nil {
			continue
		}

		if pr.MergedAt.GetTime().Before(cutoff) {
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

		prMeta := PR{
			Author:      pr.GetUser().GetLogin(),
			TimeToMerge: timeToMerge,
			Number:      pr.GetNumber(),
			Repo:        repoName,
			Reviews:     strings.Join(reviewData, "!"),
		}
		foundPrs = append(foundPrs, prMeta)
	}
	return foundPrs, nil
}
