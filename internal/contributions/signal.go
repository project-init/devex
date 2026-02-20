package contributions

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/gh"
)

type Signal struct {
	// User Info
	User string `csv:"user"`
	Repo string `csv:"repo"`

	// Raw Stats
	NumPRs           int `csv:"num_prs"`
	NumReviews       int `csv:"num_reviews"`
	totalTimeToMerge time.Duration

	// Weighted Information
	WeightedPRs     float64 `csv:"weighted_prs"`
	WeightedReviews float64 `csv:"weighted_reviews"`
	WeightedTotal   float64 `csv:"weighted_total"`

	// Merge Information
	AverageDaysToMerge float64 `csv:"average_days_to_merge"`
}

func GetGHSignal(cfg *config.Config, prs []gh.PR) {
	signalMap := getUserGHSignal(prs)
	sortedUsers := sortBySignal(signalMap)
	userSignals := make([]*Signal, len(sortedUsers))
	for index, user := range sortedUsers {
		log.Printf("User %s has signal %+v\n", user, signalMap[user])
		userSignals[index] = signalMap[user]
	}
	err := signalsToCSV(userSignals, "user", cfg)
	if err != nil {
		log.Fatal(err)
	}

	signalMap = getRepoGHSignal(prs)
	sortedRepos := sortBySignal(signalMap)
	repoSignals := make([]*Signal, len(sortedRepos))
	for index, repo := range sortedRepos {
		log.Printf("Repo %s has signal %+v\n", repo, signalMap[repo])
		repoSignals[index] = signalMap[repo]
	}
	err = signalsToCSV(repoSignals, "repo", cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func sortBySignal(signalMap map[string]*Signal) []string {
	sorted := make([]string, 0, len(signalMap))
	for user := range signalMap {
		sorted = append(sorted, user)
	}
	sort.Slice(sorted, func(i, j int) bool { return signalMap[sorted[i]].WeightedTotal > signalMap[sorted[j]].WeightedTotal })
	return sorted
}

func getUserGHSignal(prs []gh.PR) map[string]*Signal {
	signalMap := make(map[string]*Signal)
	for _, pr := range prs {
		if _, found := signalMap[pr.Author]; !found {
			signalMap[pr.Author] = &Signal{User: pr.Author}
		}
		authorSignal := signalMap[pr.Author]
		authorSignal.NumPRs++
		authorSignal.WeightedPRs += (1.0 - (.05 * float64(pr.TimeToMerge.Hours()/24))) * authorMultiplier(pr.Author)
		authorSignal.totalTimeToMerge += pr.TimeToMerge

		if pr.Reviews == "" {
			continue
		}

		reviewerPlusStates := strings.Split(pr.Reviews, "!")
		for _, reviewerPlusState := range reviewerPlusStates {
			reviewer := strings.Split(reviewerPlusState, ":")[0]
			if _, found := signalMap[reviewer]; !found {
				signalMap[reviewer] = &Signal{User: reviewer}
			}
			reviewerSignal := signalMap[reviewer]
			reviewerSignal.NumReviews++
			if isDependabot(pr.Author) {
				reviewerSignal.WeightedReviews += 0.01
			} else {
				reviewerSignal.WeightedReviews += 0.20
			}
		}
	}

	// Set the weighted total after everything is done.
	for _, signal := range signalMap {
		signal.WeightedTotal = signal.WeightedPRs + signal.WeightedReviews
		if signal.NumPRs > 0 {
			signal.AverageDaysToMerge = signal.totalTimeToMerge.Hours() / float64(signal.NumPRs) / float64(24)
		}
	}

	return signalMap
}

func getRepoGHSignal(prs []gh.PR) map[string]*Signal {
	signalMap := make(map[string]*Signal)
	for _, pr := range prs {
		if _, found := signalMap[pr.Repo]; !found {
			signalMap[pr.Repo] = &Signal{Repo: pr.Repo}
		}
		repoSignal := signalMap[pr.Repo]
		repoSignal.NumPRs++
		repoSignal.WeightedPRs += (1.0 - (.05 * float64(pr.TimeToMerge.Hours()/24))) * authorMultiplier(pr.Repo)
		repoSignal.totalTimeToMerge += pr.TimeToMerge

		if pr.Reviews == "" {
			continue
		}

		reviewerPlusStates := strings.Split(pr.Reviews, "!")
		for range reviewerPlusStates {
			repoSignal.NumReviews++
			if isDependabot(pr.Author) {
				repoSignal.WeightedReviews += 0.01
			} else {
				repoSignal.WeightedReviews += 0.20
			}
		}
	}

	// Set the weighted total after everything is done.
	for _, signal := range signalMap {
		signal.WeightedTotal = signal.WeightedPRs + signal.WeightedReviews
		if signal.NumPRs > 0 {
			signal.AverageDaysToMerge = signal.totalTimeToMerge.Hours() / float64(signal.NumPRs) / float64(24)
		}
	}

	return signalMap
}

func isDependabot(author string) bool {
	return strings.Contains(author, "[bot]")
}

func authorMultiplier(author string) float64 {
	if isDependabot(author) {
		return 0.01
	}
	return 1.0
}

func signalsToCSV(signals []*Signal, signalType string, cfg *config.Config) error {
	year, month, day := time.Now().Date()
	f, err := os.Create(fmt.Sprintf("%s/%d_%02d_%02d_%s_%d_days_signal.csv", cfg.OutputDirectories.Signals, year, month, day, signalType, cfg.NumLookBackDays))
	if err != nil {
		return err
	}

	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	bytes, err := csvutil.Marshal(signals)
	if err != nil {
		return err
	}

	_, err = f.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}
