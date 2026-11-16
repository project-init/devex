package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
)

// ErrAborted is returned when the user leaves a prompt with Ctrl-C or Esc.
var ErrAborted = errors.New("cancelled at the prompt")

// MissingFlagError is returned when a command needs an answer but cannot prompt for it.
type MissingFlagError struct {
	Flag string
}

func (e *MissingFlagError) Error() string {
	return fmt.Sprintf("pass --%s: devex prompts only when stdin and stderr are terminals", e.Flag)
}

// Option is one choice in a Select prompt.
type Option struct {
	Label string
	Value string
}

// Select asks the user to pick one option and returns its value. The option whose value is
// preselect starts highlighted. Without a terminal it fails with a MissingFlagError naming
// flag.
func (u *UI) Select(ctx context.Context, title string, options []Option, preselect, flag string) (string, error) {
	if !u.interactive {
		return "", &MissingFlagError{Flag: flag}
	}
	choices := make([]huh.Option[string], len(options))
	for index, option := range options {
		choices[index] = huh.NewOption(option.Label, option.Value)
	}
	value := preselect
	if err := u.runForm(ctx, huh.NewSelect[string]().Title(title).Options(choices...).Value(&value)); err != nil {
		return "", err
	}
	return value, nil
}

// Choose returns flagValue when it is set and names one of options, and otherwise prompts like
// Select. A flagValue outside options fails with the valid values listed.
func (u *UI) Choose(ctx context.Context, title string, options []Option, preselect, flag, flagValue string) (string, error) {
	if flagValue == "" {
		return u.Select(ctx, title, options, preselect, flag)
	}
	values := make([]string, len(options))
	for index, option := range options {
		if option.Value == flagValue {
			return flagValue, nil
		}
		values[index] = option.Value
	}
	return "", fmt.Errorf("--%s must be one of %s, got %q", flag, strings.Join(values, ", "), flagValue)
}

// Confirm asks a yes-or-no question. --yes answers it. Without a terminal and without --yes it
// fails with a MissingFlagError naming --yes.
func (u *UI) Confirm(ctx context.Context, title string) (bool, error) {
	if u.opts.Yes {
		return true, nil
	}
	if !u.interactive {
		return false, &MissingFlagError{Flag: YesFlag}
	}
	var confirmed bool
	if err := u.runForm(ctx, huh.NewConfirm().Title(title).Value(&confirmed)); err != nil {
		return false, err
	}
	return confirmed, nil
}

func (u *UI) runForm(ctx context.Context, field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).
		WithInput(u.in).
		WithOutput(u.err).
		WithShowHelp(false).
		WithAccessible(u.accessible)
	if !u.errStyled {
		form = form.WithTheme(huh.ThemeFunc(huh.ThemeBase))
	}
	if err := form.RunWithContext(ctx); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrAborted
		}
		return err
	}
	return nil
}
