package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"charm.land/bubbles/v2/spinner"
	"github.com/spf13/cobra"
)

// fakeTerminal is a stream the tests declare to be a terminal.
type fakeTerminal struct{ bytes.Buffer }

func terminalsOnly(v any) bool {
	_, ok := v.(*fakeTerminal)
	return ok
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestIsTerminalRejectsNonTerminals(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("IsTerminal(buffer) = true, want false")
	}
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if IsTerminal(file) {
		t.Error("IsTerminal(regular file) = true, want false")
	}
}

func TestStylingFollowsTerminalAndEnvironment(t *testing.T) {
	tests := []struct {
		name          string
		env           map[string]string
		opts          Options
		wantStyled    bool
		wantPrompting bool
	}{
		{name: "terminal", wantStyled: true, wantPrompting: true},
		{name: "NO_COLOR", env: map[string]string{"NO_COLOR": "1"}, wantPrompting: true},
		{name: "--no-color", opts: Options{NoColor: true}, wantPrompting: true},
		{name: "TERM=dumb", env: map[string]string{"TERM": "dumb"}, wantPrompting: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := newUI(&fakeTerminal{}, &fakeTerminal{}, &fakeTerminal{}, test.opts, env(test.env), terminalsOnly)
			if u.outStyled != test.wantStyled || u.errStyled != test.wantStyled {
				t.Errorf("styled = %v/%v, want %v", u.outStyled, u.errStyled, test.wantStyled)
			}
			if u.Interactive() != test.wantPrompting {
				t.Errorf("Interactive() = %v, want %v", u.Interactive(), test.wantPrompting)
			}
		})
	}
}

func TestPipedOutputHasNoEscapeCodes(t *testing.T) {
	var out bytes.Buffer
	errOut := &fakeTerminal{}
	u := newUI(&fakeTerminal{}, &out, errOut, Options{}, env(nil), terminalsOnly)
	u.Printf("%s %s %s\n", u.Heading("h"), u.Good("g"), u.Bad("b"))
	u.Table([]string{"PR", "Title"}, [][]string{{"#1", "feat: add"}})
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("stdout = %q, want no escape codes", out.String())
	}
	if !strings.Contains(out.String(), "PR  Title") {
		t.Errorf("stdout = %q, want space-aligned table", out.String())
	}
	u.Warn("careful")
	if !strings.Contains(errOut.String(), "\x1b") {
		t.Errorf("stderr = %q, want a styled warning on a terminal", errOut.String())
	}
}

func TestPromptsWithoutTerminalNameTheFlag(t *testing.T) {
	u := newUI(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, Options{}, env(nil), terminalsOnly)

	_, err := u.Select(context.Background(), "Environment", []Option{{Label: "staging", Value: "staging"}}, "", "env")
	var missing *MissingFlagError
	if !errors.As(err, &missing) || missing.Flag != "env" || !strings.Contains(err.Error(), "--env") {
		t.Errorf("Select() error = %v, want a MissingFlagError naming --env", err)
	}

	_, err = u.Confirm(context.Background(), "Push the tag?")
	if !errors.As(err, &missing) || missing.Flag != "yes" || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("Confirm() error = %v, want a MissingFlagError naming --yes", err)
	}
}

func TestConfirmHonorsYes(t *testing.T) {
	u := newUI(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, Options{Yes: true}, env(nil), terminalsOnly)
	confirmed, err := u.Confirm(context.Background(), "Push the tag?")
	if err != nil || !confirmed {
		t.Errorf("Confirm() = %v, %v; want true, nil", confirmed, err)
	}
}

func TestQuietKeepsWarnings(t *testing.T) {
	var errOut bytes.Buffer
	u := newUI(strings.NewReader(""), &bytes.Buffer{}, &errOut, Options{Quiet: true}, env(nil), terminalsOnly)
	u.Status("working")
	u.Success("done")
	u.Warn("careful")
	if got := errOut.String(); got != "warning: careful\n" {
		t.Errorf("stderr = %q, want only the warning", got)
	}
}

func TestWriteJSONWritesOnlyJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	u := newUI(strings.NewReader(""), &out, &errOut, Options{Format: FormatJSON}, env(nil), terminalsOnly)
	if err := u.WriteJSON(map[string]string{"version": "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded["version"] != "v1.0.0" {
		t.Errorf("stdout = %q, want JSON with the version (%v)", out.String(), err)
	}
}

func TestValidateOutputFormat(t *testing.T) {
	newCmd := func(annotated bool, output string) *cobra.Command {
		root := &cobra.Command{Use: "devex"}
		AddFlags(root)
		child := &cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}}
		if annotated {
			child.Annotations = map[string]string{JSONAnnotation: "true"}
		}
		root.AddCommand(child)
		if err := root.PersistentFlags().Set(outputFlag, output); err != nil {
			t.Fatal(err)
		}
		return child
	}
	if err := Validate(newCmd(true, "json")); err != nil {
		t.Errorf("Validate(json, annotated) = %v, want nil", err)
	}
	if err := Validate(newCmd(false, "json")); err == nil || !strings.Contains(err.Error(), "devex child supports only --output text") {
		t.Errorf("Validate(json, unannotated) = %v, want rejection naming the command", err)
	}
	if err := Validate(newCmd(false, "yaml")); err == nil || !strings.Contains(err.Error(), `got "yaml"`) {
		t.Errorf("Validate(yaml) = %v, want unknown format error", err)
	}
}

func TestProgressWithoutTerminalPrintsFinalCount(t *testing.T) {
	var errOut bytes.Buffer
	u := newUI(strings.NewReader(""), &bytes.Buffer{}, &errOut, Options{}, env(nil), terminalsOnly)
	p := u.Progress("Collecting", 2)
	p.Step("repo-a")
	p.Step("repo-b")
	p.Done()
	got := errOut.String()
	if got != "Collecting [2/2]\n" {
		t.Errorf("stderr = %q, want only the final count", got)
	}
}

func TestChooseValidatesFlagValue(t *testing.T) {
	u := newUI(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, Options{}, env(nil), terminalsOnly)
	options := []Option{{Label: "staging", Value: "staging"}, {Label: "prod", Value: "prod"}}
	if got, err := u.Choose(context.Background(), "Environment", options, "", "env", "prod"); err != nil || got != "prod" {
		t.Errorf("Choose(prod) = %q, %v; want prod", got, err)
	}
	_, err := u.Choose(context.Background(), "Environment", options, "", "env", "dev")
	if err == nil || !strings.Contains(err.Error(), "--env must be one of staging, prod") {
		t.Errorf("Choose(dev) error = %v, want the valid values", err)
	}
	var missing *MissingFlagError
	if _, err := u.Choose(context.Background(), "Environment", options, "", "env", ""); !errors.As(err, &missing) {
		t.Errorf("Choose(\"\") error = %v, want a MissingFlagError without a terminal", err)
	}
}

func TestLiveProgressHonorsNoColor(t *testing.T) {
	errOut := &fakeTerminal{}
	u := newUI(&fakeTerminal{}, &fakeTerminal{}, errOut, Options{}, env(map[string]string{"NO_COLOR": "1"}), terminalsOnly)
	p := u.Progress("Collecting", 4)
	p.Add(2, "batch")
	p.Done()
	got := errOut.String()
	if strings.Contains(got, "\x1b[38") || !strings.Contains(got, "Collecting [2/4] ############------------") {
		t.Errorf("stderr = %q, want a plain bar without color codes", got)
	}
}

func TestDumbTerminalPromptsWithoutRedraw(t *testing.T) {
	u := newUI(&fakeTerminal{}, &fakeTerminal{}, &fakeTerminal{}, Options{}, env(map[string]string{"TERM": "dumb"}), terminalsOnly)
	if u.Live() || !u.accessible || !u.Interactive() {
		t.Errorf("live=%v accessible=%v interactive=%v; want a line-based prompt and no redraw", u.Live(), u.accessible, u.Interactive())
	}
}

func TestPrintfLiftsTheLiveLine(t *testing.T) {
	shared := &fakeTerminal{} // stdout and stderr on one terminal
	u := newUI(&fakeTerminal{}, shared, shared, Options{NoColor: true}, env(nil), terminalsOnly)
	p := u.Progress("Waiting", 2)
	u.Printf("log line\n")
	p.Done()
	got := shared.String()
	if !strings.Contains(got, clearLine+"log line\nWaiting [0/2]") {
		t.Errorf("output = %q, want the live line cleared before the data and redrawn after it", got)
	}
}

func TestSpinStatusUpdatesTitleOffTerminalWithoutPanicking(t *testing.T) {
	var errOut bytes.Buffer
	u := newUI(strings.NewReader(""), &bytes.Buffer{}, &errOut, Options{}, env(nil), terminalsOnly)
	err := u.SpinStatus(context.Background(), "Waiting", func(_ context.Context, setTitle func(string)) error {
		setTitle("Waiting · RUNNING")
		return nil
	})
	if err != nil || errOut.String() != "Waiting\n" {
		t.Errorf("SpinStatus() = %v, stderr %q; want the title once", err, errOut.String())
	}
}

func TestSpinStatusRedrawsTheNewTitle(t *testing.T) {
	errOut := &fakeTerminal{}
	u := newUI(&fakeTerminal{}, &fakeTerminal{}, errOut, Options{NoColor: true}, env(nil), terminalsOnly)
	err := u.SpinStatus(context.Background(), "Waiting", func(_ context.Context, setTitle func(string)) error {
		setTitle("Waiting · RUNNING")
		time.Sleep(3 * spinner.Dot.FPS) // let the spinner draw a few frames
		return nil
	})
	if err != nil || !strings.Contains(errOut.String(), "Waiting · RUNNING") {
		t.Errorf("SpinStatus() = %v, stderr %q; want the new title drawn", err, errOut.String())
	}
}

func TestSelectPointsOutTheSuggestedOption(t *testing.T) {
	plain := newUI(&fakeTerminal{}, &fakeTerminal{}, &fakeTerminal{}, Options{NoColor: true}, env(nil), terminalsOnly)
	styled := newUI(&fakeTerminal{}, &fakeTerminal{}, &fakeTerminal{}, Options{}, env(nil), terminalsOnly)
	suggested := Option{Label: "minor", Value: "minor", Suggested: true}
	if got := plain.optionLabel(suggested); got != "minor  "+suggestedMarker {
		t.Errorf("plain label = %q, want the marker without color", got)
	}
	if got := styled.optionLabel(suggested); !strings.HasPrefix(got, "minor  \x1b[") || !strings.Contains(got, suggestedMarker) {
		t.Errorf("styled label = %q, want the marker in color", got)
	}
	if got := styled.optionLabel(Option{Label: "patch", Value: "patch"}); got != "patch" {
		t.Errorf("label = %q, want an unsuggested option unmarked", got)
	}
	dumb := newUI(&fakeTerminal{}, &fakeTerminal{}, &fakeTerminal{}, Options{}, env(map[string]string{"TERM": "dumb"}), terminalsOnly)
	if got := dumb.optionLabel(suggested); got != "minor (suggested)" {
		t.Errorf("line-based label = %q, want the marker in words", got)
	}
}

// huh wraps a row too wide for the terminal; the marker must stay on one line.
func TestSuggestedMarkerWrapsAsOneWord(t *testing.T) {
	u := newUI(&fakeTerminal{}, &fakeTerminal{}, &fakeTerminal{}, Options{NoColor: true}, env(nil), terminalsOnly)
	label := u.optionLabel(Option{Label: "patch (bug fixes, performance optimizations, code refactors)", Value: "patch", Suggested: true})
	for _, width := range []int{40, 66, 70} {
		if wrapped := ansi.Wrap(label, width, ""); !strings.Contains(wrapped, suggestedMarker) {
			t.Errorf("wrapped at %d = %q, want the marker unbroken", width, wrapped)
		}
	}
}
