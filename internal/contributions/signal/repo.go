package signal

import (
	"log"
	"math"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

func getRepoSignal(cfg *config.Config, prs []types.PR) {
	signalMapRepos := getRepoGHSignal(prs)
	sortedRepos := sortByRepoSignal(signalMapRepos)
	repoSignals := make([]*types.RepoSignal, len(sortedRepos))
	for index, repo := range sortedRepos {
		log.Printf("Repo %s has signal %+v\n", repo, signalMapRepos[repo])
		repoSignals[index] = signalMapRepos[repo]
	}
	err := signalsToCSVRepos(repoSignals, cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func sortByRepoSignal(signalMap map[string]*types.RepoSignal) []string {
	sorted := make([]string, 0, len(signalMap))
	for user := range signalMap {
		sorted = append(sorted, user)
	}
	sort.Slice(sorted, func(i, j int) bool { return signalMap[sorted[i]].WeightedTotal > signalMap[sorted[j]].WeightedTotal })
	return sorted
}

func getRepoGHSignal(prs []types.PR) map[string]*types.RepoSignal {
	signalMap := make(map[string]*types.RepoSignal)
	for _, pr := range prs {
		if _, found := signalMap[pr.Repo]; !found {
			signalMap[pr.Repo] = &types.RepoSignal{Repo: pr.Repo}
		}
		repoSignal := signalMap[pr.Repo]
		repoSignal.NumPRs++
		repoSignal.WeightedPRs += math.Max((1.0-(.05*float64(pr.TimeToMerge.Hours()/24)))*authorMultiplier(pr.Author), 0.0)
		repoSignal.TotalTimeToMerge += pr.TimeToMerge

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
			signal.AverageDaysToMerge = signal.TotalTimeToMerge.Hours() / float64(signal.NumPRs) / float64(24)
		}
		if signal.WeightedPRs > 0 {
			signal.WeightedPRShare = signal.WeightedPRs / signal.WeightedTotal
		}
		if signal.WeightedReviews > 0 {
			signal.WeightedReviewShare = signal.WeightedReviews / signal.WeightedTotal
		}
	}

	return signalMap
}
