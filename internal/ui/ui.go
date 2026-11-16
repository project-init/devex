// Package ui renders devex's terminal output and prompts so every command looks and behaves
// the same. Data goes to stdout; status, warnings, prompts, spinners, and progress go to
// stderr. Styling turns off for a stream that isn't a terminal, and everywhere when NO_COLOR
// is set, TERM is dumb, or --no-color is passed. Prompts need a terminal on stdin and stderr;
// without one they fail and name the flag that answers them instead.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Format is the value of --output.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

const (
	outputFlag  = "output"
	quietFlag   = "quiet"
	noColorFlag = "no-color"
)

// YesFlag is the global flag that answers confirmations.
const YesFlag = "yes"

// JSONAnnotation marks a command that supports --output json. Validate rejects json for any
// command without it.
const JSONAnnotation = "devex.ui/json"

// AddFlags registers the global output flags on the root command.
func AddFlags(root *cobra.Command) {
	flags := root.PersistentFlags()
	flags.StringP(outputFlag, "o", string(FormatText), "output format: text or json")
	flags.BoolP(quietFlag, "q", false, "print only data, warnings, and errors")
	flags.Bool(noColorFlag, false, "disable color and styling")
	flags.BoolP(YesFlag, "y", false, "answer yes to every confirmation")
}

// Validate rejects an unknown --output value, and json on a command without JSONAnnotation.
func Validate(cmd *cobra.Command) error {
	switch format := Format(flagString(cmd, outputFlag)); format {
	case "", FormatText:
		return nil
	case FormatJSON:
		if cmd.Annotations[JSONAnnotation] != "true" {
			return fmt.Errorf("%s supports only --output text", cmd.CommandPath())
		}
		return nil
	default:
		return fmt.Errorf("--output must be text or json, got %q", format)
	}
}

// Options are the global flag values a UI honors.
type Options struct {
	Format  Format
	Quiet   bool
	Yes     bool
	NoColor bool
}

// UI writes one command's output. Its methods are safe for concurrent use.
type UI struct {
	in   io.Reader
	out  io.Writer
	err  io.Writer
	opts Options

	outStyled   bool // stdout takes color and borders
	errStyled   bool // stderr takes color
	interactive bool // stdin and stderr are terminals, so prompts can run
	accessible  bool // prompts read plain lines because the terminal can't redraw
	live        bool // stderr can redraw a spinner or progress line in place
	errWidth    int  // stderr's width in columns when live, or 0 when unknown

	mu       sync.Mutex // serializes stderr writes
	liveLine string     // the spinner or progress line currently drawn on stderr
}

// New builds a UI from cmd's streams and the global flags.
func New(cmd *cobra.Command) *UI {
	return NewWithStreams(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), Options{
		Format:  Format(flagString(cmd, outputFlag)),
		Quiet:   flagBool(cmd, quietFlag),
		Yes:     flagBool(cmd, YesFlag),
		NoColor: flagBool(cmd, noColorFlag),
	})
}

// NewWithStreams builds a UI on explicit streams, for callers and tests outside Cobra.
func NewWithStreams(in io.Reader, out, err io.Writer, opts Options) *UI {
	return newUI(in, out, err, opts, os.Getenv, IsTerminal)
}

func newUI(in io.Reader, out, errOut io.Writer, opts Options, getenv func(string) string, isTerminal func(any) bool) *UI {
	if opts.Format == "" {
		opts.Format = FormatText
	}
	// A dumb terminal can't take color or redraw a line, but it can still answer a line-based
	// prompt.
	dumb := getenv("TERM") == "dumb"
	color := !opts.NoColor && getenv("NO_COLOR") == "" && !dumb
	errTerminal := isTerminal(errOut)
	u := &UI{
		in:          in,
		out:         out,
		err:         errOut,
		opts:        opts,
		outStyled:   color && isTerminal(out),
		errStyled:   color && errTerminal,
		interactive: errTerminal && isTerminal(in),
		accessible:  dumb,
		live:        errTerminal && !dumb && !opts.Quiet,
	}
	if u.live {
		u.errWidth = terminalWidth(errOut)
	}
	return u
}

// IsTerminal reports whether v is a file attached to a terminal.
func IsTerminal(v any) bool {
	file, ok := v.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

// Out is stdout, for data.
func (u *UI) Out() io.Writer { return u.out }

// Err is stderr, for diagnostics.
func (u *UI) Err() io.Writer { return u.err }

// JSON reports whether the command should write JSON instead of text.
func (u *UI) JSON() bool { return u.opts.Format == FormatJSON }

// Yes reports whether --yes answers confirmations.
func (u *UI) Yes() bool { return u.opts.Yes }

// Interactive reports whether the UI can prompt.
func (u *UI) Interactive() bool { return u.interactive }

// Live reports whether stderr can redraw a line in place, for spinners and progress.
func (u *UI) Live() bool { return u.live }

// Printf writes data to stdout.
func (u *UI) Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(u.out, format, args...)
}

// WriteJSON writes v to stdout as indented JSON.
func (u *UI) WriteJSON(v any) error {
	encoder := json.NewEncoder(u.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

// Status writes a progress message to stderr unless --quiet is set.
func (u *UI) Status(format string, args ...any) {
	if u.opts.Quiet {
		return
	}
	u.writeErr(fmt.Sprintf(format, args...))
}

// Success writes a completion message to stderr unless --quiet is set.
func (u *UI) Success(format string, args ...any) {
	u.Status("%s %s", u.errStyle(goodStyle, "✓"), fmt.Sprintf(format, args...))
}

// Warn writes a warning to stderr, even under --quiet.
func (u *UI) Warn(format string, args ...any) {
	u.writeErr(u.errStyle(warnStyle, "warning:") + " " + fmt.Sprintf(format, args...))
}

// writeErr writes one line to stderr, lifting any live line out of the way and redrawing it
// underneath.
func (u *UI) writeErr(line string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.liveLine != "" {
		_, _ = io.WriteString(u.err, clearLine)
	}
	_, _ = io.WriteString(u.err, line+"\n")
	if u.liveLine != "" {
		_, _ = io.WriteString(u.err, u.liveLine)
	}
}

func flagString(cmd *cobra.Command, name string) string {
	if flag := cmd.Flag(name); flag != nil {
		return flag.Value.String()
	}
	return ""
}

func flagBool(cmd *cobra.Command, name string) bool {
	return flagString(cmd, name) == "true"
}
