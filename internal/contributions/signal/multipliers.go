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

	// Helper: is weekend for a bucket index i (relative to asOfDay).
	isWeekendIdx := func(i int) bool {
		d := asOfDay.AddDate(0, 0, -i)
		w := d.Weekday()
		return w == time.Saturday || w == time.Sunday
	}

	// Count weekdays in window (denominators should ignore weekend "missing" days).
	weekdayDays := 0
	for i := 0; i < maxDays; i++ {
		if !isWeekendIdx(i) {
			weekdayDays++
		}
	}
	if weekdayDays == 0 {
		// Extremely unlikely unless maxDays==0, but be safe.
		return 0
	}

	// Active days counts:
	// - activeAll: any day with activity (weekends included, so weekend work helps)
	// - activeWeekdays: weekday activity only (used for some burst heuristics)
	activeAll := 0
	activeWeekdays := 0

	maxDaily := 0
	peakIdx := -1

	for i, c := range counts {
		if c > 0 {
			activeAll++
			if !isWeekendIdx(i) {
				activeWeekdays++
			}
			if c > maxDaily {
				maxDaily = c
				peakIdx = i
			}
		}
	}

	// 1) Coverage: distinct active days across the window, but don't penalize weekends.
	// Weekend contributions help because activeAll includes them, but denominator excludes weekends.
	coverage := float64(activeAll) / float64(weekdayDays)
	coverage = clamp01(coverage)

	// 2) Evenness: best when PRs are spread across days (weekend work can only help).
	den := float64(minInt(total, weekdayDays))
	evenness := float64(activeAll) / den
	evenness = clamp01(evenness)

	base := math.Sqrt(coverage) * math.Sqrt(evenness)
	base = clamp01(base)

	// --- Burst-after-inactivity penalty ---
	// Compute "leading zeros" ignoring weekends entirely so weekend gaps don't look like inactivity.
	leadingZerosOldestWeekdays := 0
	for i := maxDays - 1; i >= 0; i-- {
		if isWeekendIdx(i) {
			continue
		}
		if counts[i] == 0 {
			leadingZerosOldestWeekdays++
			continue
		}
		break
	}

	lateStartFrac := float64(leadingZerosOldestWeekdays) / float64(weekdayDays)
	concentration := float64(maxDaily) / float64(total)

	// "Surrounded" check (still in calendar-day space). This is mostly about shape, not weekends.
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
		strength := 1.6 * lateStartFrac * concentration
		if strength > 0.85 {
			strength = 0.85
		}
		penalty = 1.0 - strength
	} else if concentration >= 0.65 && activeWeekdays <= weekdayDays/6 && !burstSurrounded {
		// Mild penalty for very bursty patterns, weekend-neutral.
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
