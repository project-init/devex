package signal

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

func getUserSignal(cfg *config.Config, prs []types.PR) error {
	signalMap, err := getUserGHSignal(cfg, prs)
	if err != nil {
		return err
	}

	sortedUsers := sortByUserSignal(signalMap)
	userSignals := make([]*types.UserSignal, len(sortedUsers))
	for index, user := range sortedUsers {
		userSignals[index] = signalMap[user]
	}
	err = signalsToCSVUsers(userSignals, cfg)
	if err != nil {
		return err
	}
	return nil
}

func sortByUserSignal(signalMap map[string]*types.UserSignal) []string {
	sorted := make([]string, 0, len(signalMap))
	for user := range signalMap {
		sorted = append(sorted, user)
	}
	sort.Slice(sorted, func(i, j int) bool { return signalMap[sorted[i]].WeightedTotal > signalMap[sorted[j]].WeightedTotal })
	return sorted
}

func getUserGHSignal(cfg *config.Config, prs []types.PR) (map[string]*types.UserSignal, error) {
	signalMap := make(map[string]*types.UserSignal)
	weightModifierByAuthor := distributionWeightsByAuthor(prs, cfg.NumLookBackDays)
	for _, pr := range prs {
		if _, found := signalMap[pr.Author]; !found {
			signalMap[pr.Author] = &types.UserSignal{User: pr.Author}
		}
		authorSignal := signalMap[pr.Author]
		authorSignal.NumPRs++
		authorSignal.WeightedPRs += math.Max((1.0-(.05*float64(pr.TimeToMerge.Hours()/24)))*authorMultiplier(weightModifierByAuthor, pr.Author), 0.0)
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
			if isBot(pr.Author) {
				reviewerSignal.WeightedReviews += 0.01
			} else {
				reviewerSignal.WeightedReviews += 0.20
			}
		}
	}

	// Set the weighted total after everything is done.
	for _, signal := range signalMap {
		modifier, found := weightModifierByAuthor[signal.User]
		if !found && !strings.Contains(signal.User, "[bot]") {
			// Most likely the user only reviewed content in this repo so give them a base level modifier.
			modifier = 0.75
			fmt.Printf("user signal (%s) weight modifier not found, using %2f\n", signal.User, modifier)
		}
		signal.WeightedDistributionModifier = modifier
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

	return signalMap, nil
}
