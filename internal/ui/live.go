package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// clearLine returns the cursor to column one and erases the line.
const clearLine = "\r\x1b[2K"

const progressBarWidth = 24

// Spin runs fn while a spinner labelled title turns on stderr. Without a live terminal it
// prints title once as a status line instead. Ctrl-C still reaches the process, which
// cancels ctx.
func (u *UI) Spin(ctx context.Context, title string, fn func(context.Context) error) error {
	return u.SpinStatus(ctx, title, func(ctx context.Context, _ func(string)) error { return fn(ctx) })
}

// SpinStatus is Spin with a label fn can change as it works, such as a task's status and elapsed
// time. Off a live terminal, label changes are dropped.
func (u *UI) SpinStatus(ctx context.Context, title string, fn func(ctx context.Context, setTitle func(string)) error) error {
	if !u.live {
		u.Status("%s", title)
		return fn(ctx, func(string) {})
	}
	glyphs := make([]string, len(spinner.Dot.Frames))
	for index, glyph := range spinner.Dot.Frames {
		glyphs[index] = u.errStyle(accentStyle, glyph)
	}
	var current atomic.Pointer[string]
	current.Store(&title)
	setTitle := func(text string) { current.Store(&text) }
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(spinner.Dot.FPS)
		defer ticker.Stop()
		for frame := 0; ; frame++ {
			u.setLive(glyphs[frame%len(glyphs)] + " " + *current.Load())
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	})
	err := fn(ctx, setTitle)
	close(done)
	wg.Wait()
	u.clearLive()
	return err
}

// Progress tracks a known number of steps. Call Step as each finishes and Done at the end.
type Progress struct {
	ui    *UI
	title string
	total int
	start time.Time
	bar   *progress.Model // nil when stderr takes no color; line draws a plain bar instead

	mu   sync.Mutex
	done int
}

// Progress starts a progress display of total steps. On a live terminal it redraws one line
// with a bar, a count, and an ETA. Otherwise Done prints the final count once, so logs stay
// short.
func (u *UI) Progress(title string, total int) *Progress {
	p := &Progress{ui: u, title: title, total: total, start: time.Now()}
	if u.live {
		if u.errStyled {
			bar := progress.New(progress.WithWidth(progressBarWidth), progress.WithoutPercentage(),
				progress.WithColors(accentStyle.GetForeground()))
			p.bar = &bar
		}
		u.setLive(p.line(""))
	}
	return p
}

// Step records one finished step, described by label.
func (p *Progress) Step(label string) {
	p.Add(1, label)
}

// Add records n finished steps, described by label.
func (p *Progress) Add(n int, label string) {
	// Drawing under p.mu keeps concurrent steps from drawing an older count over a newer one.
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	if p.ui.live {
		p.ui.setLive(p.line(label))
	}
}

// Done removes the live progress line, or off a terminal prints the final count.
func (p *Progress) Done() {
	if p.ui.live {
		p.ui.clearLive()
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ui.Status("%s", p.line(""))
}

// line renders the progress line; the caller holds p.mu.
func (p *Progress) line(label string) string {
	parts := []string{p.title, fmt.Sprintf("[%d/%d]", p.done, p.total)}
	if p.ui.live && p.total > 0 {
		parts = append(parts, p.renderBar(float64(p.done)/float64(p.total)))
	}
	if eta := p.eta(); eta != "" {
		parts = append(parts, "ETA "+eta)
	}
	if label != "" {
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

func (p *Progress) renderBar(fraction float64) string {
	if p.bar != nil {
		return p.bar.ViewAs(fraction)
	}
	filled := int(fraction * progressBarWidth)
	filled = min(max(filled, 0), progressBarWidth)
	return strings.Repeat("#", filled) + strings.Repeat("-", progressBarWidth-filled)
}

func (p *Progress) eta() string {
	if p.done == 0 || p.done >= p.total {
		return ""
	}
	elapsed := time.Since(p.start)
	remaining := elapsed / time.Duration(p.done) * time.Duration(p.total-p.done)
	return remaining.Round(time.Second).String()
}

// setLive draws line in place of the current live line. It cuts line to the terminal width,
// because a wrapped line leaves rows that the next redraw cannot erase.
func (u *UI) setLive(line string) {
	if u.errWidth > 1 {
		line = ansi.Truncate(line, u.errWidth-1, "…")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.liveLine = line
	_, _ = io.WriteString(u.err, clearLine+line)
}

func (u *UI) clearLive() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.liveLine != "" {
		_, _ = io.WriteString(u.err, clearLine)
	}
	u.liveLine = ""
}

// terminalWidth is w's terminal width in columns, or 0 when unknown.
func terminalWidth(w io.Writer) int {
	file, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil {
		return 0
	}
	return width
}
