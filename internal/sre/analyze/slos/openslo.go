package slos

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// openSLOVersion is the OpenSLO apiVersion devex reads, https://openslo.com/specification/.
const openSLOVersion = "openslo/v1"

// openSLOSources are the metricSource types devex has a source for.
var openSLOSources = []string{"datadog"}

// occurrencesSlice is the bucket an Occurrences SLO's counts are summed in. The budget counts events, not slices, so
// the size changes only how many points a query returns.
const occurrencesSlice = time.Hour

// groupedQuery matches a Datadog query split by tag, which returns a series per group. devex reads one series per
// query, so the groups would run together as if they were slices.
var groupedQuery = regexp.MustCompile(`\bby\s*\{`)

// The subset of an OpenSLO v1 SLO that devex reads: an inline indicator, one rolling time window, and one objective.
type openSLO struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string         `yaml:"name"`
		DisplayName string         `yaml:"displayName"`
		Labels      map[string]any `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Service   string `yaml:"service"`
		Indicator *struct {
			Spec struct {
				ThresholdMetric *openSLOMetric `yaml:"thresholdMetric"`
				RatioMetric     *struct {
					Good  *openSLOMetric `yaml:"good"`
					Bad   *openSLOMetric `yaml:"bad"`
					Total *openSLOMetric `yaml:"total"`
				} `yaml:"ratioMetric"`
			} `yaml:"spec"`
		} `yaml:"indicator"`
		IndicatorRef string `yaml:"indicatorRef"`
		TimeWindow   []struct {
			Duration  string `yaml:"duration"`
			IsRolling bool   `yaml:"isRolling"`
		} `yaml:"timeWindow"`
		BudgetingMethod string `yaml:"budgetingMethod"`
		Objectives      []struct {
			Op              string   `yaml:"op"`
			Value           *float64 `yaml:"value"`
			Target          float64  `yaml:"target"`
			TimeSliceWindow string   `yaml:"timeSliceWindow"`
		} `yaml:"objectives"`
	} `yaml:"spec"`
}

type openSLOMetric struct {
	MetricSource struct {
		Type string `yaml:"type"`
		Spec struct {
			Query string `yaml:"query"`
		} `yaml:"spec"`
	} `yaml:"metricSource"`
}

// parseOpenSLO reads the SLO documents whose spec.service is group, in file order, and converts each into devex's own
// type. group may be empty when every SLO has the same service. Documents of another kind, such as Service, are
// skipped. Anything outside the subset devex reads fails with what to change, rather than being scored wrong.
func parseOpenSLO(data []byte, group string) ([]slo, error) {
	groups := map[string][]slo{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc openSLO
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the OpenSLO file: %w", err)
		}
		if doc.Kind != "SLO" {
			continue
		}
		s, err := doc.convert()
		if err != nil {
			return nil, fmt.Errorf("SLO %q: %w", doc.Metadata.Name, err)
		}
		groups[doc.Spec.Service] = append(groups[doc.Spec.Service], s)
	}
	return pickGroup(groups, group)
}

func (d openSLO) convert() (slo, error) {
	s := slo{ID: d.Metadata.Name, Display: d.Metadata.DisplayName, Service: d.Spec.Service, Tags: labelTags(d.Metadata.Labels)}
	spec := d.Spec
	switch {
	case d.APIVersion != openSLOVersion:
		return s, fmt.Errorf("apiVersion is %q: devex reads %s", d.APIVersion, openSLOVersion)
	case s.ID == "":
		return s, errors.New("metadata.name is empty")
	case spec.Service == "":
		return s, errors.New("spec.service is empty: it is the group --group picks")
	case spec.IndicatorRef != "" || spec.Indicator == nil:
		return s, errors.New("give the indicator inline under spec.indicator: devex does not resolve indicatorRef")
	case len(spec.TimeWindow) != 1 || !spec.TimeWindow[0].IsRolling:
		return s, errors.New("give one rolling spec.timeWindow")
	case len(spec.Objectives) != 1:
		return s, fmt.Errorf("has %d objectives: devex reads one", len(spec.Objectives))
	}
	window, err := openSLODuration(spec.TimeWindow[0].Duration)
	if err != nil {
		return s, fmt.Errorf("spec.timeWindow: %w", err)
	}
	s.Window = window
	obj := spec.Objectives[0]
	if obj.Target <= 0 || obj.Target >= 1 {
		return s, fmt.Errorf("objective target %v: want a share between 0 and 1, e.g. 0.95", obj.Target)
	}
	s.Target = obj.Target

	ind := spec.Indicator.Spec
	switch spec.BudgetingMethod {
	case "Timeslices":
		if ind.ThresholdMetric == nil {
			return s, errors.New("budgetingMethod Timeslices needs a thresholdMetric indicator")
		}
		if obj.Op != "lt" || obj.Value == nil || *obj.Value <= 0 {
			return s, errors.New("budgetingMethod Timeslices needs an objective with op: lt and a value above 0; a slice at the value counts as bad")
		}
		if s.Slice, err = openSLODuration(obj.TimeSliceWindow); err != nil {
			return s, fmt.Errorf("objective timeSliceWindow: %w", err)
		}
		s.Budgeting, s.Threshold = timeslices, *obj.Value
		if s.Indicator.Query, err = openSLOQuery(ind.ThresholdMetric, "thresholdMetric"); err != nil {
			return s, err
		}
	case "Occurrences":
		r := ind.RatioMetric
		switch {
		case r == nil:
			return s, errors.New("budgetingMethod Occurrences needs a ratioMetric indicator")
		case r.Bad != nil || r.Good == nil || r.Total == nil:
			return s, errors.New("give the ratioMetric as good and total: devex does not read bad")
		}
		s.Budgeting, s.Slice = occurrences, occurrencesSlice
		if s.Indicator.Good, err = openSLOQuery(r.Good, "ratioMetric.good"); err != nil {
			return s, err
		}
		if s.Indicator.Total, err = openSLOQuery(r.Total, "ratioMetric.total"); err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("budgetingMethod %q: devex reads Timeslices and Occurrences", spec.BudgetingMethod)
	}
	return s, nil
}

func openSLOQuery(m *openSLOMetric, field string) (string, error) {
	if t := strings.ToLower(m.MetricSource.Type); !slices.Contains(openSLOSources, t) {
		return "", fmt.Errorf("%s metricSource type %q: devex reads %s", field, m.MetricSource.Type, strings.Join(openSLOSources, ", "))
	}
	q := m.MetricSource.Spec.Query
	switch {
	case q == "":
		return "", fmt.Errorf("%s has no metricSource.spec.query", field)
	case groupedQuery.MatchString(q):
		return "", fmt.Errorf("%s query %q is split by a tag: give one series, and an SLO per group", field, q)
	case strings.Contains(q, ".rollup(") || strings.Contains(q, ".as_count("):
		return "", fmt.Errorf("%s query %q has .rollup() or .as_count(): leave them out, devex adds them per slice", field, q)
	}
	return q, nil
}

// openSLOUnits are the OpenSLO duration units a rolling window uses.
var openSLOUnits = map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": day, "w": 7 * day}

// openSLODuration reads an OpenSLO duration: 15m, 1h, 30d, 4w.
func openSLODuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("duration %q: want a number and m, h, d, or w, e.g. 30d", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	unit, ok := openSLOUnits[s[len(s)-1:]]
	if err != nil || !ok || n < 1 {
		return 0, fmt.Errorf("duration %q: want a number and m, h, d, or w, e.g. 30d", s)
	}
	return time.Duration(n) * unit, nil
}

// labelTags reads the tags label, which OpenSLO allows as one string or a list: p0,p1 or [p0, p1].
func labelTags(labels map[string]any) []string {
	switch v := labels["tags"].(type) {
	case string:
		var out []string
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
		return out
	case []any:
		var out []string
		for _, t := range v {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// pickGroup returns the SLOs of group. An empty group picks the only one, and names the groups when there are more.
func pickGroup(groups map[string][]slo, group string) ([]slo, error) {
	names := strings.Join(slices.Sorted(maps.Keys(groups)), ", ")
	switch {
	case len(groups) == 0:
		return nil, errors.New("the file has no SLO documents")
	case group == "" && len(groups) == 1:
		for _, slos := range groups {
			return slos, nil
		}
	case group == "":
		return nil, fmt.Errorf("pass --group: the file holds the SLOs of %s", names)
	}
	slos, ok := groups[group]
	if !ok {
		return nil, fmt.Errorf("the file has no SLO of service %q: pass --group with one of %s", group, names)
	}
	return slos, nil
}

// filterByTags keeps the SLOs that carry any of tags. No tags keeps every SLO.
func filterByTags(slos []slo, tags []string) []slo {
	if len(tags) == 0 {
		return slos
	}
	return slices.DeleteFunc(slices.Clone(slos), func(s slo) bool {
		return !slices.ContainsFunc(s.Tags, func(t string) bool { return slices.Contains(tags, t) })
	})
}
