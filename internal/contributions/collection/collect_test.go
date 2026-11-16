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

func TestCollectResumesShortlyBeforeTheLastMerge(t *testing.T) {
	dir := t.TempDir()
	lastMerge := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	beforeWindow := time.Now().Add(-40 * 24 * time.Hour).UTC().Truncate(time.Second)
	collected := []types.PR{{Author: "octocat", Repo: "devex", Number: 1, MergedAt: lastMerge}, {Author: "octocat", Repo: "devex", Number: 0, MergedAt: beforeWindow}}
	if err := writePrs(filepath.Join(dir, "devex.csv"), collected); err != nil {
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
	if want := lastMerge.Add(-resumeOverlap); !fetcher.since["devex"].Equal(want) {
		t.Errorf("fetched since %s, want shortly before the last collected merge, %s", fetcher.since["devex"], want)
	}
	prs, err := loadPrs(filepath.Join(dir, "devex.csv"))
	if err != nil || len(prs) != 3 || prs[0].Number != 2 {
		t.Errorf("devex.csv = %#v, %v; want every pull request, newest first", prs, err)
	}
}

// A file whose merges start inside the window may come from a shorter one, so collect fetches the
// whole window again rather than leave its start missing.
func TestCollectBackfillsAWindowItNeverCovered(t *testing.T) {
	dir := t.TempDir()
	lastMerge := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	collectedPR := types.PR{Author: "octocat", Repo: "devex", Number: 1, MergedAt: lastMerge}
	if err := writePrs(filepath.Join(dir, "devex.csv"), []types.PR{collectedPR}); err != nil {
		t.Fatal(err)
	}
	older := types.PR{Author: "monalisa", Repo: "devex", Number: 2, MergedAt: lastMerge.Add(-60 * 24 * time.Hour)}
	cfg := &config.Config{NumLookBackDays: 90, OutputDirectories: config.OutputDirectories{Prs: dir}}
	fetcher := &fakeFetcher{
		repos: []string{"devex"},
		prs:   map[string][]types.PR{"devex": {collectedPR, older}},
		since: map[string]time.Time{},
	}
	before := cfg.CutoffDate()
	if err := collect(context.Background(), quietUI(), cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	// Either cutoff passes, in case the run crossed midnight UTC.
	if got, after := fetcher.since["devex"], cfg.CutoffDate(); !got.Equal(before) && !got.Equal(after) {
		t.Errorf("fetched since %s, want the cutoff %s", got, before)
	}
	prs, err := loadPrs(filepath.Join(dir, "devex.csv"))
	if err != nil || len(prs) != 2 || prs[0].Number != 1 || prs[1].Number != 2 {
		t.Errorf("devex.csv = %#v, %v; want each pull request once, newest first", prs, err)
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

// coveredFile writes a file whose merges reach back past a 30-day window, with pr the latest.
func coveredFile(t *testing.T, pr types.PR) (string, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	old := types.PR{Author: "octocat", Repo: "devex", Number: 1, MergedAt: time.Now().Add(-40 * 24 * time.Hour).UTC().Truncate(time.Second)}
	if err := writePrs(filepath.Join(dir, "devex.csv"), []types.PR{pr, old}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "devex.csv"), &config.Config{NumLookBackDays: 30, OutputDirectories: config.OutputDirectories{Prs: dir}}
}

func TestCollectSavesAPullRequestThatChanged(t *testing.T) {
	collected := types.PR{Author: "octocat", Repo: "devex", Number: 2, MergedAt: time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)}
	path, cfg := coveredFile(t, collected)
	refreshed := collected
	refreshed.Reviews = "hubot:APPROVED"
	fetcher := &fakeFetcher{repos: []string{"devex"}, prs: map[string][]types.PR{"devex": {refreshed}}, since: map[string]time.Time{}}
	if err := collect(context.Background(), quietUI(), cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	prs, err := loadPrs(path)
	if err != nil || len(prs) != 2 || prs[0].Reviews != "hubot:APPROVED" {
		t.Errorf("devex.csv = %#v, %v; want the refreshed reviews saved", prs, err)
	}
}

func TestCollectLeavesAnUnchangedFileAlone(t *testing.T) {
	collected := types.PR{Author: "octocat", Repo: "devex", Number: 2, MergedAt: time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)}
	path, cfg := coveredFile(t, collected)
	written := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	same := collected
	same.MergedAt = collected.MergedAt.In(time.FixedZone("CST", -6*60*60))
	fetcher := &fakeFetcher{repos: []string{"devex"}, prs: map[string][]types.PR{"devex": {same}}, since: map[string]time.Time{}}
	if err := collect(context.Background(), quietUI(), cfg, fetcher); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(written) {
		t.Errorf("devex.csv rewritten (%v), want it left alone when nothing changed", err)
	}
}

func TestResumeFromStaysInsideTheWindow(t *testing.T) {
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	prs := []types.PR{{Number: 1, MergedAt: cutoff.Add(-time.Hour)}, {Number: 2, MergedAt: cutoff.Add(time.Minute)}}
	if got := resumeFrom(prs, cutoff); !got.Equal(cutoff) {
		t.Errorf("resumeFrom() = %s, want the cutoff %s, not the overlap before it", got, cutoff)
	}
}
