package signal

import (
	"log"
	"math"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

func getUserSignal(cfg *config.Config, prs []types.PR) {
	signalMap := getUserGHSignal(prs)
	sortedUsers := sortByUserSignal(signalMap)
	userSignals := make([]*types.UserSignal, len(sortedUsers))
	for index, user := range sortedUsers {
		log.Printf("User %s has signal %+v\n", user, signalMap[user])
		userSignals[index] = signalMap[user]
	}
	err := signalsToCSVUsers(userSignals, cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func sortByUserSignal(signalMap map[string]*types.UserSignal) []string {
	sorted := make([]string, 0, len(signalMap))
	for user := range signalMap {
		sorted = append(sorted, user)
	}
	sort.Slice(sorted, func(i, j int) bool { return signalMap[sorted[i]].WeightedTotal > signalMap[sorted[j]].WeightedTotal })
	return sorted
}

func getUserGHSignal(prs []types.PR) map[string]*types.UserSignal {
	signalMap := make(map[string]*types.UserSignal)
	for _, pr := range prs {
		if _, found := signalMap[pr.Author]; !found {
			signalMap[pr.Author] = &types.UserSignal{User: pr.Author}
		}
		authorSignal := signalMap[pr.Author]
		authorSignal.NumPRs++
		authorSignal.WeightedPRs += math.Max((1.0-(.05*float64(pr.TimeToMerge.Hours()/24)))*authorMultiplier(pr.Repo), 0.0)
		authorSignal.TotalTimeToMerge += pr.TimeToMerge

		if pr.Reviews == "" {
			continue
		}

		reviewerPlusStates := strings.Split(pr.Reviews, "!")
		for _, reviewerPlusState := range reviewerPlusStates {
			reviewer := strings.Split(reviewerPlusState, ":")[0]
			if _, found := signalMap[reviewer]; !found {
				signalMap[reviewer] = &types.UserSignal{User: reviewer}
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
