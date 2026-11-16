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
	"github.com/project-init/devex/internal/atomicfile"
	"github.com/project-init/devex/internal/contributions/collection/gh"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
	"github.com/project-init/devex/internal/ui"
	"golang.org/x/sync/errgroup"
)

// concurrency is how many repositories collect reads at once.
const concurrency = 4

// resumeOverlap is how far before a repository's latest collected merge a rerun starts, so a pull
// request merged in the same second, or that GitHub listed late, still arrives.
const resumeOverlap = time.Hour

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

// collect adds each repository's newly merged pull requests to its file, reading several
// repositories at once. It writes a repository's file as soon as that repository finishes, when
// the file is missing or its pull requests changed.
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
			fetched, err := github.GetRepoPRs(ctx, resumeFrom(prs, cutoffDate), repo)
			if err != nil {
				return err
			}
			allPrs, added, changed := mergePrs(prs, fetched)
			if changed || !collected {
				if err := writePrs(path, allPrs); err != nil {
					return err
				}
			}
			label := fmt.Sprintf("%s: %d new pull requests", repo, added)
			if !u.Live() {
				u.Status("%s", label)
			}
			progress.Step(label)
			return nil
		})
	}
	return group.Wait()
}

// resumeFrom returns where to fetch a repository's merges from. Collected merges that reach back
// past cutoff cover the window, so fetching resumes shortly before the latest one; otherwise it
// starts at cutoff, which backfills a window that grew since the last collection.
func resumeFrom(prs []types.PR, cutoff time.Time) time.Time {
	covered, latest := false, cutoff
	for _, pr := range prs {
		covered = covered || !pr.MergedAt.After(cutoff)
		if pr.MergedAt.After(latest) {
			latest = pr.MergedAt
		}
	}
	if since := latest.Add(-resumeOverlap); covered && since.After(cutoff) {
		return since
	}
	return cutoff
}

// mergePrs adds fetched pull requests to the collected ones, newest merge first, replacing any it
// collected before. It reports how many it added, and whether anything changed.
func mergePrs(collected, fetched []types.PR) ([]types.PR, int, bool) {
	index := make(map[int]int, len(collected))
	for i, pr := range collected {
		index[pr.Number] = i
	}
	added, changed := 0, false
	for _, pr := range fetched {
		if i, ok := index[pr.Number]; ok {
			changed = changed || !samePR(collected[i], pr)
			collected[i] = pr
			continue
		}
		index[pr.Number] = len(collected)
		collected = append(collected, pr)
		added++
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].MergedAt.After(collected[j].MergedAt)
	})
	return collected, added, changed || added > 0
}

// samePR reports whether a and b hold the same values, comparing merge times as instants rather
// than by time zone.
func samePR(a, b types.PR) bool {
	a.MergedAt, b.MergedAt = a.MergedAt.UTC(), b.MergedAt.UTC()
	return a == b
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

// writePrs replaces filePath atomically, so an interrupted run leaves the last complete
// collection for the next one to resume from.
func writePrs(filePath string, prs []types.PR) error {
	bytes, err := csvutil.Marshal(prs)
	if err != nil {
		return err
	}
	return atomicfile.Write(filePath, bytes)
}
