package signal

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

// botReviewPRs pair a human pull request reviewed by a bot and a human with a bot pull request
// reviewed by the same human.
var botReviewPRs = []types.PR{
	{Author: "octocat", Repo: "devex", MergedAt: time.Now(), TimeToMerge: time.Hour, Reviews: "dependabot[bot]:COMMENTED!monalisa:APPROVED"},
	{Author: "renovate[bot]", Repo: "devex", MergedAt: time.Now(), TimeToMerge: time.Hour, Reviews: "monalisa:APPROVED"},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestUserSignalWeighsReviewsByTheReviewer(t *testing.T) {
	signals, err := getUserGHSignal(&config.Config{NumLookBackDays: 30}, botReviewPRs)
	if err != nil {
		t.Fatal(err)
	}
	if got := signals["dependabot[bot]"].WeightedReviews; !near(got, 0.01) {
		t.Errorf("bot reviewer weight = %v, want 0.01", got)
	}
	if got := signals["monalisa"].WeightedReviews; !near(got, 0.40) {
		t.Errorf("human reviewer weight = %v, want 0.40 for two reviews, one on a bot's pull request", got)
	}
}

func TestRepoSignalWeighsReviewsByTheReviewer(t *testing.T) {
	// A human's pull request with one bot review and one human review.
	if got := getRepoGHSignal(botReviewPRs[:1])["devex"].WeightedReviews; !near(got, 0.21) {
		t.Errorf("repo review weight = %v, want 0.21", got)
	}
}

func TestRunPointsAtCollectWhenNothingWasCollected(t *testing.T) {
	cfg := &config.Config{OutputDirectories: config.OutputDirectories{Prs: t.TempDir() + "/missing", Signals: t.TempDir()}}
	if _, err := Run(cfg); err == nil || !strings.Contains(err.Error(), "run devex contributions collect first") {
		t.Errorf("Run() error = %v, want a pointer to collect", err)
	}
}
