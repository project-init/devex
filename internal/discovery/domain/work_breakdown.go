package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = "v1"

type ItemID string

type ItemKind string

const (
	KindInitiative ItemKind = "initiative"
	KindFeature    ItemKind = "feature"
	KindTask       ItemKind = "task"
	KindDefect     ItemKind = "defect"
	KindResearch   ItemKind = "research"
)

type WorkBreakdown struct {
	SchemaVersion string       `yaml:"schema_version" json:"schema_version"`
	Discovery     DiscoveryRef `yaml:"discovery" json:"discovery"`
	Items         []WorkItem   `yaml:"items" json:"items"`
}

type DiscoveryRef struct {
	ID       string `yaml:"id" json:"id"`
	Title    string `yaml:"title" json:"title"`
	Document string `yaml:"document" json:"document"`
}

type WorkItem struct {
	ID                 ItemID    `yaml:"id" json:"id"`
	Kind               ItemKind  `yaml:"kind" json:"kind"`
	Parent             ItemID    `yaml:"parent,omitempty" json:"parent,omitempty"`
	Title              string    `yaml:"title" json:"title"`
	Description        string    `yaml:"description" json:"description"`
	AcceptanceCriteria []string  `yaml:"acceptance_criteria,omitempty" json:"acceptance_criteria,omitempty"`
	DependsOn          []ItemID  `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Labels             []string  `yaml:"labels,omitempty" json:"labels,omitempty"`
	Estimate           *Estimate `yaml:"estimate,omitempty" json:"estimate,omitempty"`
}

type Estimate struct {
	Value float64      `yaml:"value" json:"value"`
	Unit  EstimateUnit `yaml:"unit" json:"unit"`
}

type EstimateUnit string

const (
	EstimatePoints EstimateUnit = "points"
	EstimateHours  EstimateUnit = "hours"
	EstimateDays   EstimateUnit = "days"
	EstimateWeeks  EstimateUnit = "weeks"
)

var idPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*$`)

func (w *WorkBreakdown) Validate() error {
	var problems []string
	if w.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version must be %q", SchemaVersion))
	}
	if strings.TrimSpace(w.Discovery.ID) == "" {
		problems = append(problems, "discovery.id is required")
	}
	if strings.TrimSpace(w.Discovery.Title) == "" {
		problems = append(problems, "discovery.title is required")
	}
	if strings.TrimSpace(w.Discovery.Document) == "" {
		problems = append(problems, "discovery.document is required")
	}
	if len(w.Items) == 0 {
		problems = append(problems, "items must contain at least one work item")
	}

	byID := make(map[ItemID]WorkItem, len(w.Items))
	initiativeCount := 0
	for index, item := range w.Items {
		prefix := fmt.Sprintf("items[%d]", index)
		if !idPattern.MatchString(string(item.ID)) {
			problems = append(problems, prefix+".id must use uppercase letters, digits, underscores, or hyphens")
		}
		if _, exists := byID[item.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate item id %q", item.ID))
		}
		byID[item.ID] = item
		if !validKind(item.Kind) {
			problems = append(problems, fmt.Sprintf("%s.kind %q is not supported", prefix, item.Kind))
		}
		if item.Kind == KindInitiative {
			initiativeCount++
			if item.Parent != "" {
				problems = append(problems, fmt.Sprintf("initiative %q cannot have a parent", item.ID))
			}
		}
		if strings.TrimSpace(item.Title) == "" {
			problems = append(problems, prefix+".title is required")
		}
		if strings.TrimSpace(item.Description) == "" {
			problems = append(problems, prefix+".description is required")
		}
		if item.Parent == item.ID && item.Parent != "" {
			problems = append(problems, fmt.Sprintf("item %q cannot be its own parent", item.ID))
		}
		for _, dependency := range item.DependsOn {
			if dependency == item.ID {
				problems = append(problems, fmt.Sprintf("item %q cannot depend on itself", item.ID))
			}
		}
		if item.Estimate != nil && !validEstimate(*item.Estimate) {
			problems = append(problems, fmt.Sprintf("item %q has an invalid estimate", item.ID))
		}
	}

	if initiativeCount == 0 {
		problems = append(problems, "items must contain at least one initiative")
	}
	for _, item := range w.Items {
		if item.Parent != "" {
			if _, exists := byID[item.Parent]; !exists {
				problems = append(problems, fmt.Sprintf("item %q references unknown parent %q", item.ID, item.Parent))
			}
		}
		for _, dependency := range item.DependsOn {
			if _, exists := byID[dependency]; !exists {
				problems = append(problems, fmt.Sprintf("item %q references unknown dependency %q", item.ID, dependency))
				continue
			}
			if isAncestor(byID, item, dependency) {
				problems = append(
					problems,
					fmt.Sprintf("item %q cannot depend on its ancestor %q", item.ID, dependency),
				)
			}
		}
	}

	if _, err := w.OrderedItems(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("invalid work breakdown:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// isAncestor reports whether candidate sits on item's parent chain. Hierarchy already orders that
// work, and providers publish dependencies as blocking links, so such an edge would make a parent
// block its own child. The visited set keeps a parent cycle from looping here before
// OrderedItems reports it.
func isAncestor(byID map[ItemID]WorkItem, item WorkItem, candidate ItemID) bool {
	visited := make(map[ItemID]bool, len(byID))
	for parent := item.Parent; parent != ""; {
		if visited[parent] {
			return false
		}
		visited[parent] = true
		if parent == candidate {
			return true
		}
		next, exists := byID[parent]
		if !exists {
			return false
		}
		parent = next.Parent
	}
	return false
}

func (w *WorkBreakdown) OrderedItems() ([]WorkItem, error) {
	byID := make(map[ItemID]WorkItem, len(w.Items))
	for _, item := range w.Items {
		byID[item.ID] = item
	}

	state := make(map[ItemID]uint8, len(w.Items))
	ordered := make([]WorkItem, 0, len(w.Items))
	var visit func(ItemID) error
	visit = func(id ItemID) error {
		item, exists := byID[id]
		if !exists {
			return nil
		}
		switch state[id] {
		case 1:
			return fmt.Errorf("parent or dependency cycle includes %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		if item.Parent != "" {
			if err := visit(item.Parent); err != nil {
				return err
			}
		}
		for _, dependency := range item.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		ordered = append(ordered, item)
		return nil
	}
	for _, item := range w.Items {
		if err := visit(item.ID); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func validKind(kind ItemKind) bool {
	switch kind {
	case KindInitiative, KindFeature, KindTask, KindDefect, KindResearch:
		return true
	default:
		return false
	}
}

func validEstimate(estimate Estimate) bool {
	if estimate.Value <= 0 {
		return false
	}
	switch estimate.Unit {
	case EstimatePoints, EstimateHours, EstimateDays, EstimateWeeks:
		return true
	default:
		return false
	}
}
