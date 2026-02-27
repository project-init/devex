package types

import "time"

type UserSignal struct {
	// User Info
	User string `csv:"user"`

	// Weighted Information
	WeightedTotal                float64 `csv:"weighted_total"`
	WeightedPRs                  float64 `csv:"weighted_prs"`
	WeightedReviews              float64 `csv:"weighted_reviews"`
	WeightedPRShare              float64 `csv:"weighted_pr_share"`
	WeightedReviewShare          float64 `csv:"weighted_review_share"`
	WeightedDistributionModifier float64 `csv:"weighted_distribution_modifier"`

	// Raw Stats
	NumPRs     int `csv:"num_prs"`
	NumReviews int `csv:"num_reviews"`

	// Scoring Use Only
	TotalTimeToMerge time.Duration `csv:"-"`

	// Merge Information
	AverageDaysToMerge float64 `csv:"average_days_to_merge"`
}

type RepoSignal struct {
	// User Info
	Repo string `csv:"repo"`

	// Weighted Information
	WeightedTotal       float64 `csv:"weighted_total"`
	WeightedPRs         float64 `csv:"weighted_prs"`
	WeightedReviews     float64 `csv:"weighted_reviews"`
	WeightedPRShare     float64 `csv:"weighted_pr_share"`
	WeightedReviewShare float64 `csv:"weighted_review_share"`

	// Raw Stats
	NumPRs     int `csv:"num_prs"`
	NumReviews int `csv:"num_reviews"`

	// Scoring Use Only
	TotalTimeToMerge time.Duration `csv:"-"`

	// Merge Information
	AverageDaysToMerge float64 `csv:"average_days_to_merge"`
}
