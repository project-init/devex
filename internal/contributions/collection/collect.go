package collection

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/collection/gh"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
	"github.com/project-init/devex/internal/ui"
	"golang.org/x/sync/errgroup"
)

// concurrency is how many repositories collect reads at once.
const concurrency = 4

// fetcher reads repositories and their pull requests from GitHub.
type fetcher interface {
	GetRepos(ctx context.Context, cfg *config.Config) ([]string, error)
	GetRepoPRs(ctx context.Context, cutoff time.Time, repo string) ([]types.PR, error)
}

// Run collects the organization's merged pull requests into cfg's PR directory, one CSV per
// repository.
func Run(ctx context.Context, u *ui.UI, cfg *config.Config) error {
	github, err := gh.New(cfg.Organization, u.Warn)
	if err != nil {
		return err
	}
	return collect(ctx, u, cfg, github)
}

// collect writes each repository's pull requests merged since its last collection, reading
// several repositories at once. It rewrites a repository's file as soon as that repository
// finishes.
func collect(ctx context.Context, u *ui.UI, cfg *config.Config, github fetcher) error {
	if err := os.MkdirAll(cfg.OutputDirectories.Prs, 0o755); err != nil {
		return err
	}
	var repos []string
	err := u.Spin(ctx, "Listing repositories", func(ctx context.Context) error {
		var err error
		repos, err = github.GetRepos(ctx, cfg)
		return err
	})
	if err != nil {
		return err
	}
	cutoffDate := cfg.CutoffDate()
	u.Status("Collecting pull requests merged since %s from %d repositories", cutoffDate.Format(time.DateOnly), len(repos))

	progress := u.Progress("Repositories", len(repos))
	defer progress.Done()
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(concurrency)
	for _, repo := range repos {
		group.Go(func() error {
			path := filepath.Join(cfg.OutputDirectories.Prs, repo+".csv")
			prs, err := loadPrs(path)
			collected := err == nil
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			since := cutoffDate
			for _, pr := range prs {
				if pr.MergedAt.After(since) {
					since = pr.MergedAt
				}
			}
			newPrs, err := github.GetRepoPRs(ctx, since, repo)
			if err != nil {
				return err
			}
			if len(newPrs) > 0 || !collected {
				allPrs := append(prs, newPrs...)
				sort.Slice(allPrs, func(i, j int) bool {
					return allPrs[i].MergedAt.After(allPrs[j].MergedAt)
				})
				if err := writePrs(path, allPrs); err != nil {
					return err
				}
			}
			label := fmt.Sprintf("%s: %d new pull requests", repo, len(newPrs))
			if !u.Live() {
				u.Status("%s", label)
			}
			progress.Step(label)
			return nil
		})
	}
	return group.Wait()
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

		repoName := strings.TrimSuffix(entry.Name(), ".csv")
		loadedPrs, err := loadPrs(filepath.Join(cfg.OutputDirectories.Prs, entry.Name()))
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

// tempSuffix ends writePrs's temporary files. It keeps GetExistingPRs from reading one that a
// killed run left behind as a repository's.
const tempSuffix = ".tmp"

// writePrs replaces filePath through a temporary file, so an interrupted run leaves the last
// complete collection for the next one to resume from. A new file gets the mode os.Create would
// give it; a replaced one keeps its mode.
func writePrs(filePath string, prs []types.PR) error {
	bytes, err := csvutil.Marshal(prs)
	if err != nil {
		return err
	}
	// The process ID keeps two runs on one directory apart; a leftover with this process's ID is
	// a killed run's. Creating it with 0666 lets the umask apply, as os.Create did.
	tempPath := filepath.Join(filepath.Dir(filePath), fmt.Sprintf(".%s.%d%s", filepath.Base(filePath), os.Getpid(), tempSuffix))
	_ = os.Remove(tempPath)
	info, statErr := os.Stat(filePath)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	temp, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if statErr == nil {
		if err := temp.Chmod(info.Mode().Perm()); err != nil {
			_ = temp.Close()
			return err
		}
	}
	if _, err := temp.Write(bytes); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, filePath)
}
