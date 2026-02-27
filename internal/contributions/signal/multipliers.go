package signal

import (
	"log"
	"math"
	"strings"
	"time"

	"github.com/project-init/devex/internal/contributions/types"
)

func isBot(author string) bool {
	return strings.Contains(author, "[bot]")
}

func authorMultiplier(authorModifiers map[string]float64, author string) float64 {
	if isBot(author) {
		return 0.01
	}

	// Repos
	if authorModifiers == nil {
		return 1.0
	}

	modifier, found := authorModifiers[author]
	if !found {
		log.Print("author multiplier not found for", author)
		return 1.0
	}
	return modifier
}

// distributionWeightsByAuthor computes a [0..1] consistency/distribution weight per author
// over the last maxDays relative to asOf.
//
// Heuristic summary:
//   - Coverage: how many distinct days had PRs (fraction of maxDays), sqrt-boosted.
//   - Evenness: activeDays / min(totalPRs, maxDays) (1.0 when ~1 PR/day across the window).
//   - Burst-after-inactivity penalty: triggers mainly when there is a long leading gap of zero PR days
//     AND PRs are concentrated into very few days AND the burst isn't surrounded by activity.
func distributionWeightsByAuthor(prs []types.PR, maxDays int) map[string]float64 {
	asOf := time.Now()
	out := map[string]float64{}
	if maxDays <= 0 || len(prs) == 0 {
		return out
	}

	byAuthor := map[string][]types.PR{}
	for _, pr := range prs {
		if pr.Author == "" {
			continue
		}
		byAuthor[pr.Author] = append(byAuthor[pr.Author], pr)
	}

	for author, list := range byAuthor {
		out[author] = authorDistributionWeight(list, asOf, maxDays)
	}

	return out
}

func authorDistributionWeight(prs []types.PR, asOf time.Time, maxDays int) float64 {
	// Day buckets, relative to asOf.
	// Index 0 = today, 1 = yesterday, ... maxDays-1 = oldest day in window.
	counts := make([]int, maxDays)

	loc := asOf.Location()
	asOfDay := time.Date(asOf.In(loc).Year(), asOf.In(loc).Month(), asOf.In(loc).Day(), 0, 0, 0, 0, loc)

	total := 0
	for _, pr := range prs {
		t := pr.MergedAt.In(loc)
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)

		ageDays := int(math.Floor(asOfDay.Sub(day).Hours() / 24.0))
		if ageDays < 0 || ageDays >= maxDays {
			continue
		}
		counts[ageDays]++
		total++
	}

	if total == 0 {
		return 0
	}

	activeDays := 0
	maxDaily := 0
	peakIdx := -1
	for i, c := range counts {
		if c > 0 {
			activeDays++
			if c > maxDaily {
				maxDaily = c
				peakIdx = i
			}
		}
	}

	// 1) Coverage: distinct active days across the window.
	coverage := float64(activeDays) / float64(maxDays)
	coverage = clamp01(coverage)

	// 2) Evenness: best when PRs are spread across days instead of stacked.
	//    Uses min(total, maxDays) so you can still get 1.0 even if total > maxDays,
	//    as long as you're active on essentially all days.
	den := float64(minInt(total, maxDays))
	evenness := float64(activeDays) / den
	evenness = clamp01(evenness)

	// Base score: "sustained work is worth ~double" feel via sqrt(coverage).
	base := math.Sqrt(coverage) * math.Sqrt(evenness)
	base = clamp01(base)

	// --- Burst-after-inactivity penalty ---
	//
	// We want to punish:
	//   [0 PRs for a long time] -> [burst]
	// But we do NOT want to punish:
	//   [consistent] -> [burst] -> [consistent]
	//
	// We'll detect:
	// - leadingZerosOldest: count of oldest consecutive days with zero PRs (started late)
	// - concentration: fraction of PRs on busiest day
	// - burstSurrounded: activity exists clearly both before and after the peak day
	leadingZerosOldest := 0
	for i := maxDays - 1; i >= 0; i-- {
		if counts[i] == 0 {
			leadingZerosOldest++
			continue
		}
		break
	}
	lateStartFrac := float64(leadingZerosOldest) / float64(maxDays)
	concentration := float64(maxDaily) / float64(total)

	// "Surrounded" check: activity at least gap days older AND at least gap days newer than peak.
	// This avoids treating spillover adjacent to the burst as "consistent on both sides".
	const gap = 3
	hasOlder := false // indices > peakIdx+gap
	hasNewer := false // indices < peakIdx-gap
	if peakIdx >= 0 {
		for i := peakIdx + gap + 1; i < maxDays; i++ {
			if counts[i] > 0 {
				hasOlder = true
				break
			}
		}
		for i := 0; i <= peakIdx-gap-1; i++ {
			if counts[i] > 0 {
				hasNewer = true
				break
			}
		}
	}
	burstSurrounded := hasOlder && hasNewer

	penalty := 1.0

	// Strong penalty for "nothing then burst"
	if lateStartFrac >= 0.40 && concentration >= 0.40 && !burstSurrounded {
		// Scales with how late the start was and how concentrated the burst is.
		// Caps at 85% reduction.
		strength := 1.6 * lateStartFrac * concentration
		if strength > 0.85 {
			strength = 0.85
		}
		penalty = 1.0 - strength
	} else if concentration >= 0.65 && activeDays <= maxDays/6 && !burstSurrounded {
		// Mild penalty for very bursty patterns even without a huge late gap.
		penalty = 0.85
	}

	return clamp01(base * penalty)
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
