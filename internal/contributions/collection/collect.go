package collection

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/collection/gh"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

func Run(ctx context.Context, cfg *config.Config) error {
	github, err := gh.New(cfg.Organization)
	if err != nil {
		return err
	}

	repos, err := github.GetRepos(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Collecting from %d repositories:\n", len(repos))
	for _, repo := range repos {
		log.Printf("\t%s", repo)
	}

	cutoffDate := cfg.CutoffDate()
	log.Printf("initial cutoff date: %s\n", cutoffDate)

	existingPrs, err := GetExistingPRs(cfg)
	if err != nil {
		return err
	}

	for _, repo := range repos {
		mostRecentPR := cutoffDate
		prs := existingPrs[repo]
		for _, pr := range prs {
			if pr.MergedAt.After(mostRecentPR) {
				mostRecentPR = pr.MergedAt
			}
		}

		log.Printf("getting PRs for %s, cutoff date of: %s\n", repo, mostRecentPR)
		newPrs, err := github.GetRepoPRs(ctx, mostRecentPR, repo, cfg)
		if err != nil {
			return err
		}

		// Merge and Sort the PRs
		allPrs := append(prs, newPrs...)
		sort.Slice(allPrs, func(i, j int) bool {
			return allPrs[i].MergedAt.After(allPrs[j].MergedAt)
		})

		// Spit back out in to a file
		err = writePrs(fmt.Sprintf("%s/%s.csv", cfg.OutputDirectories.Prs, repo), allPrs)
		if err != nil {
			return err
		}
	}

	return nil
}

func GetExistingPRs(cfg *config.Config) (map[string][]types.PR, error) {
	dir, err := os.ReadDir(cfg.OutputDirectories.Prs)
	if err != nil {
		return nil, err
	}

	prMap := make(map[string][]types.PR)
	for _, entry := range dir {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".csv") {
			continue
		}

		repoName := strings.ReplaceAll(entry.Name(), ".csv", "")
		loadedPrs, err := loadPrs(fmt.Sprintf("%s/%s", cfg.OutputDirectories.Prs, entry.Name()))
		if err != nil {
			return nil, err
		}
		prMap[repoName] = loadedPrs
	}

	return prMap, nil
}

func loadPrs(filePath string) ([]types.PR, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("open csv: %w", err)
	}

	var prs []types.PR
	err = csvutil.Unmarshal(data, &prs)
	if err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return prs, nil
}

func writePrs(filePath string, prs []types.PR) error {
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}

	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	bytes, err := csvutil.Marshal(prs)
	if err != nil {
		return err
	}

	_, err = f.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}
