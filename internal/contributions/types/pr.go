package types

import "time"

type PR struct {
	Author      string        `csv:"author"`
	TimeToMerge time.Duration `csv:"time_to_merge_duration"`
	MergedAt    time.Time     `csv:"merged_at"`
	Repo        string        `csv:"repo"`
	Number      int           `csv:"number"`
	Reviews     string        `csv:"reviews"`
}
