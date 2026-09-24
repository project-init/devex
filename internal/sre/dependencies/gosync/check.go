package gosync

import (
	"cmp"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

// Problem is a pin that breaks the sync invariant.
type Problem struct {
	Pin    pins.Pin
	Reason string
}

// ProblemsError reports every pin that breaks the sync invariant.
type ProblemsError struct {
	Title    string
	Problems []Problem
}

func (e *ProblemsError) Error() string {
	var b strings.Builder
	b.WriteString(e.Title + "\n")
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, p := range e.Problems {
		_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", p.Pin.Location(), p.Pin.Kind, p.Pin.Version, p.Reason)
	}
	_ = w.Flush()

	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}

	return strings.Join(lines, "\n")
}

// Current derives the version every toolchain pin agrees on. The most precise pin decides,
// and every other toolchain pin must agree with it at its own precision. With no toolchain
// pins, the highest go directive stands in, as the oldest toolchain every module accepts.
func Current(found []pins.Pin) (goversion.Version, error) {
	var toolchain, floors []pins.Pin
	for _, p := range found {
		if p.IsToolchain() {
			toolchain = append(toolchain, p)
		} else {
			floors = append(floors, p)
		}
	}

	if len(toolchain) == 0 {
		if len(floors) == 0 {
			return goversion.Version{}, errors.New("found no Go version pins and no go.mod")
		}

		return slices.MaxFunc(floors, func(a, b pins.Pin) int { return a.Version.Compare(b.Version) }).Version, nil
	}

	current := slices.MaxFunc(toolchain, func(a, b pins.Pin) int { return cmp.Compare(a.Version.Precision, b.Version.Precision) }).Version
	for _, p := range toolchain {
		if !p.Version.Agrees(current) {
			drift := make([]Problem, len(toolchain))
			for i, q := range toolchain {
				drift[i] = Problem{Pin: q}
			}

			return goversion.Version{}, &ProblemsError{Title: "go version drift: toolchain pins disagree", Problems: drift}
		}
	}

	return current, nil
}

// Check verifies the sync invariant offline: toolchain pins agree, every go directive sits
// at or below the toolchain, no go.work floor sits below a module it uses, and the directive
// policy holds.
func Check(found pins.Result, directive string) (goversion.Version, error) {
	workspaceErr := problemsErr("go.work go directives below a module they use; the go command refuses to build these workspaces", workspaceProblems(found))
	current, err := Current(found.Pins)
	if err != nil {
		return goversion.Version{}, errors.Join(err, workspaceErr)
	}
	directiveErr := problemsErr(fmt.Sprintf("go directives out of step with toolchain %s", current), directiveProblems(found.Pins, current, directive))

	return current, errors.Join(directiveErr, workspaceErr)
}

// problemsErr returns a ProblemsError for problems, or nil when there are none.
func problemsErr(title string, problems []Problem) error {
	if len(problems) == 0 {
		return nil
	}

	return &ProblemsError{Title: title, Problems: problems}
}

func workspaceProblems(found pins.Result) []Problem {
	var problems []Problem
	for _, r := range workspaceRaises(found) {
		problems = append(problems, Problem{Pin: found.Pins[r.index], Reason: fmt.Sprintf("below %s's go %s", r.module.File, r.module.Version)})
	}

	return problems
}

// workspaceRaise pairs the go.work go directive at index in found.Pins with the highest go
// directive among the modules it uses, which it sits below.
type workspaceRaise struct {
	index  int
	module pins.Pin
}

func workspaceRaises(found pins.Result) []workspaceRaise {
	floors := map[string]pins.Pin{}
	for _, p := range found.Pins {
		if p.Kind == pins.KindGoDirective {
			floors[path.Dir(p.File)] = p
		}
	}

	var raises []workspaceRaise
	for i, work := range found.Pins {
		if work.Kind != pins.KindWorkGo {
			continue
		}
		var highest pins.Pin
		for _, dir := range found.Workspaces[work.File] {
			if mod, ok := floors[dir]; ok && (highest.File == "" || highest.Version.Precedes(mod.Version)) {
				highest = mod
			}
		}
		if highest.File != "" && work.Version.Precedes(highest.Version) {
			raises = append(raises, workspaceRaise{index: i, module: highest})
		}
	}

	return raises
}

func directiveProblems(found []pins.Pin, toolchain goversion.Version, directive string) []Problem {
	var problems []Problem
	for _, p := range found {
		if p.IsToolchain() {
			continue
		}
		switch {
		case !p.Version.AtMost(toolchain):
			problems = append(problems, Problem{Pin: p, Reason: fmt.Sprintf("above toolchain %s", toolchain)})
		case directive == DirectiveExact && !p.Version.Agrees(toolchain):
			problems = append(problems, Problem{Pin: p, Reason: fmt.Sprintf("directive exact wants %s", toolchain)})
		case directive == DirectiveMinor && !p.Version.SameMinor(toolchain):
			problems = append(problems, Problem{Pin: p, Reason: fmt.Sprintf("directive minor wants %s", toolchain.MinorFloor())})
		}
	}

	return problems
}
