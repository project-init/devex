package slos

import (
	"cmp"
	"time"
)

const day = 24 * time.Hour

// budgeting is how an SLO counts good and bad: by time slice, or by event.
type budgeting int

const (
	// timeslices counts a slice good when its value is under the threshold, e.g. a 15m slice's p95 under 500.
	timeslices budgeting = iota
	// occurrences counts good events out of all events, e.g. requests that did not fail.
	occurrences
)

// slo is one objective, in devex's own shape. An OpenSLO document converts into it right after parsing, and every
// metrics source reads it, so the scoring knows neither how the SLO was written nor which backend holds its data.
type slo struct {
	ID      string // metadata.name
	Display string // metadata.displayName, which the report shows when it is set
	Service string // spec.service, which --group picks
	Tags    []string

	Budgeting budgeting
	// Threshold is the value a timeslice must stay under, in the indicator's own unit. It is 0 for occurrences.
	Threshold float64
	Target    float64       // share of slices or events that must be good, e.g. 0.95
	Window    time.Duration // the period the error budget covers, e.g. 30 days
	Slice     time.Duration // the bucket size of the SLO's queries
	Indicator indicator
}

// indicator is what a metrics source reads for an SLO, written in the source's own language: Query for a threshold,
// Good and Total for a ratio.
type indicator struct {
	Query, Good, Total string
}

// Name is the SLO as the report shows it.
func (s slo) Name() string {
	return cmp.Or(s.Display, s.ID)
}
