package collection

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
	"github.com/project-init/devex/internal/ui"
)

type fakeFetcher struct {
	repos []string
	prs   map[string][]types.PR

	mu      sync.Mutex
	since   map[string]time.Time
	running int
	peak    int
}

func (f *fakeFetcher) GetRepos(context.Context, *config.Config) ([]string, error) {
	return f.repos, nil
}

func (f *fakeFetcher) GetRepoPRs(_ context.Context, cutoff time.Time, repo string) ([]types.PR, error) {
	f.mu.Lock()
	f.since[repo] = cutoff
	f.running++
	f.peak = max(f.peak, f.running)
	f.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	f.running--
	f.mu.Unlock()
	return f.prs[repo], nil
}

func TestCollectCreatesTheOutputDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "prs")
	cfg := &config.Config{NumLookBackDays: 30, OutputDirectories: config.OutputDirectories{Prs: dir}}
	fetcher := &fakeFetcher{repos: []string{"devex"}, since: map[string]time.Time{}}
	if err := collect(context.Background(), quietUI(), cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "devex.csv")); err != nil {
		t.Errorf("devex.csv: %v", err)
	}
}

func TestCollectReadsRepositoriesSideBySide(t *testing.T) {
	repos := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	cfg := &config.Config{NumLookBackDays: 30, OutputDirectories: config.OutputDirectories{Prs: t.TempDir()}}
	fetcher := &fakeFetcher{repos: repos, since: map[string]time.Time{}}
	if err := collect(context.Background(), quietUI(), cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	if fetcher.peak < 2 || fetcher.peak > concurrency {
		t.Errorf("peak concurrent repositories = %d, want between 2 and %d", fetcher.peak, concurrency)
	}
}

func TestCollectResumesAfterTheLastMerge(t *testing.T) {
	dir := t.TempDir()
	lastMerge := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	if err := writePrs(filepath.Join(dir, "devex.csv"), []types.PR{{Author: "octocat", Repo: "devex", Number: 1, MergedAt: lastMerge}}); err != nil {
		t.Fatal(err)
	}
	newMerge := lastMerge.Add(time.Hour)
	cfg := &config.Config{NumLookBackDays: 30, OutputDirectories: config.OutputDirectories{Prs: dir}}
	fetcher := &fakeFetcher{
		repos: []string{"devex"},
		prs:   map[string][]types.PR{"devex": {{Author: "monalisa", Repo: "devex", Number: 2, MergedAt: newMerge}}},
		since: map[string]time.Time{},
	}
	if err := collect(context.Background(), quietUI(), cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	if !fetcher.since["devex"].Equal(lastMerge) {
		t.Errorf("fetched since %s, want the last collected merge %s", fetcher.since["devex"], lastMerge)
	}
	prs, err := loadPrs(filepath.Join(dir, "devex.csv"))
	if err != nil || len(prs) != 2 || prs[0].Number != 2 {
		t.Errorf("devex.csv = %#v, %v; want both pull requests, newest first", prs, err)
	}
}

func TestGetExistingPRsSkipsALeftoverTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, ".devex.csv.123-1.tmp") // atomicfile's temporary name
	if err := os.WriteFile(leftover, []byte("half a csv"), 0o644); err != nil {
		t.Fatal(err)
	}
	prs, err := GetExistingPRs(&config.Config{OutputDirectories: config.OutputDirectories{Prs: dir}})
	if err != nil || len(prs) != 0 {
		t.Errorf("GetExistingPRs() = %v, %v; want the leftover ignored", prs, err)
	}
}

func quietUI() *ui.UI {
	return ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{})
}
