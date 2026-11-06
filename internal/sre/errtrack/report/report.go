// Package report defines the JSON document errtrack emits. That document is the
// contract between this tool and its two consumers: a coding agent working the
// error, and the ticket-creation step that files it.
//
// Two conventions hold throughout. Every key in the schema is always present —
// nullable values are pointers rather than omitted, so a consumer can tell
// "unknown" from "absent" without guessing. And timestamps are time.Time, which
// marshals to RFC 3339; providers convert their own wire formats on the way in.
//
// The rule for pointers: a field is a pointer when its zero value could be
// mistaken for a real one. 0 is a plausible occurrence count and a plausible
// line number, and a zero time.Time prints as a date, so those are pointers.
// "" is not a plausible service name, so strings are plain and empty means
// unknown.
//
// Nil slices marshal to null rather than []. Consumers handle that.
package report

import "time"

// SchemaVersion is the version of the document this package emits. Bump it when
// a change would break a consumer.
const SchemaVersion = 1

const (
	// SummaryMaxRunes caps Summary. Jira accepts 255, but a shorter line reads
	// better in an issue list. Counted in runes, not bytes — error messages
	// carry user data and will eventually be non-ASCII.
	SummaryMaxRunes = 120

	// MaxTicketFrames is how many stack frames a ticket carries. The full stack
	// is rarely useful on an issue; the top few almost always are.
	MaxTicketFrames = 3
)

// Source names the provider a report came from, so consumers can switch on it
// when the provider-specific sections differ.
type Source string

const SourceDatadog Source = "datadog"

// Report is the full document written to stdout.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	Source        Source    `json:"source"`
	FetchedAt     time.Time `json:"fetched_at"`

	// Summary is ready to use as a Jira summary: "[service] type: message",
	// truncated to SummaryMaxRunes.
	Summary string `json:"summary"`

	Error       Failure     `json:"error"`
	Timeline    Timeline    `json:"timeline"`
	Occurrences Occurrences `json:"occurrences"`
	Code        Code        `json:"code"`
	Links       Links       `json:"links"`
}

// Failure identifies what went wrong.
//
// Named Failure rather than Error on purpose: a type called error in this
// package would shadow the builtin interface, and every function here that
// returns an error would silently mean something else.
type Failure struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Service string `json:"service"`

	// Environment comes from the --env flag. Datadog's Error Tracking API
	// carries no environment field on either the issue or its samples, so it
	// cannot be fetched and is null when the caller does not supply it.
	Environment *string `json:"environment"`

	State     string   `json:"state"`
	IsCrash   bool     `json:"is_crash"`
	Platform  string   `json:"platform"`
	Languages []string `json:"languages"`
}

// Timeline is when the error was first and last seen, with the application
// version — usually a git sha — observed at each point.
//
// The timestamps are pointers: a zero time.Time marshals to
// "0001-01-01T00:00:00Z", which reads like a real date on a ticket. Null says
// "the provider did not tell us" and cannot be mistaken for anything else.
// Report.FetchedAt is not a pointer because this tool sets it, so it is never
// unknown.
type Timeline struct {
	FirstSeen        *time.Time `json:"first_seen"`
	FirstSeenVersion string     `json:"first_seen_version"`
	LastSeen         *time.Time `json:"last_seen"`
	LastSeenVersion  string     `json:"last_seen_version"`
}

// Occurrences is how often the error happened.
//
// Every field is nullable because Datadog serves these only from the issue
// search endpoint, not from a single issue lookup — a report built from one
// lookup leaves them null rather than reporting a misleading zero.
type Occurrences struct {
	Total            *int `json:"total"`
	ImpactedUsers    *int `json:"impacted_users"`
	ImpactedSessions *int `json:"impacted_sessions"`
}

// Code locates the error in the source tree.
type Code struct {
	RepositoryURL string   `json:"repository_url"`
	CommitSHA     string   `json:"commit_sha"`
	Location      Location `json:"location"`

	// Frames holds at most MaxTicketFrames frames, innermost first.
	// FramesTruncated reports whether any were dropped, so a reader can tell
	// the last frame shown from the bottom of the stack.
	Frames          []Frame `json:"frames"`
	FramesTruncated bool    `json:"frames_truncated"`
}

// Location is the single place a ticket leads with.
type Location struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Line     *int   `json:"line"`
}

// Frame is one stack frame.
type Frame struct {
	Text      string `json:"text"`
	File      string `json:"file"`
	FirstLine *int   `json:"first_line"`
	LastLine  *int   `json:"last_line"`

	// SourceURL is derived rather than fetched: the repository, commit, path
	// and line assembled into a permalink. Empty when the host is not
	// recognised, since a wrong link on a ticket is worse than none.
	SourceURL string `json:"source_url"`

	// CodeSnippet is nil when the provider could not resolve the source.
	CodeSnippet *CodeSnippet `json:"code_snippet"`
}

// CodeSnippet is the source around a frame. Offset is the line number of
// Lines[0], so a renderer can number the block correctly.
type CodeSnippet struct {
	Offset int      `json:"offset"`
	Lines  []string `json:"lines"`
}

// Links points back at the provider, for whoever reads the ticket.
type Links struct {
	Issue         string  `json:"issue"`
	Trace         *string `json:"trace"`
	SessionReplay *string `json:"session_replay"`
}
