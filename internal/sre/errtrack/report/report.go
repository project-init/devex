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

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

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
	// RepositoryURL and CommitSHA describe the service: the repository it is
	// built from and the commit the failing version was built at. Each Frame
	// carries its own pair, because a frame can sit in another repository —
	// a vendored dependency or a shared module — and those are what its
	// SourceURL is built from. These stay here so a report whose provider
	// returned no frames still says where the service lives.
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

	// RepositoryURL and CommitSHA are where this frame's file actually lives,
	// which is not always where the service lives: Datadog resolves a
	// repository and a ref per frame, so a vendored dependency or a shared
	// module comes back with its own. A provider that gives no per-frame value
	// falls back to the service's, so a consumer reads these without needing
	// to know which it got.
	RepositoryURL string `json:"repository_url"`
	CommitSHA     string `json:"commit_sha"`

	// SourceURL is derived rather than fetched: this frame's repository,
	// commit, path and line assembled into a permalink. Empty when the host is
	// not recognised, since a wrong link on a ticket is worse than none.
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

// Summarize builds the Jira summary line, "[service] type: message", capped at
// SummaryMaxRunes.
//
// Truncation counts runes rather than bytes: error messages carry user data, so
// one will eventually hold a multi-byte character, and cutting a string mid-rune
// emits invalid UTF-8 into the document.
func Summarize(service, errType, message string) string {
	summary := fmt.Sprintf("[%s] %s: %s", service, errType, message)

	runes := []rune(summary)
	if len(runes) <= SummaryMaxRunes {
		return summary
	}

	// One rune of the budget goes to the ellipsis, so the result still fits.
	return string(runes[:SummaryMaxRunes-1]) + "…"
}

// githubHost is the only forge SourceURL knows how to build a permalink for.
const githubHost = "github.com"

// SourceURL assembles a permalink to the line a stack frame points at:
//
//	https://github.com/dailypay/gigapi/blob/<sha>/internal/transfers/service.go#L142
//
// It returns "" rather than a guess whenever it cannot build a link it is sure
// of — an unknown forge, a missing commit, no line. A dead link on a ticket
// costs more than an absent one, because someone follows it before noticing.
//
// Only GitHub is supported. The path segment differs per forge (GitLab inserts
// /-/ before blob, Bitbucket uses /src/) and so does the line anchor
// (Bitbucket wants #lines-42), so a GitHub-shaped URL aimed at either one 404s.
//
// The commit is required, never a branch: a link to a branch shows whatever
// that line holds today, which after a refactor is different code or past the
// end of the file.
func SourceURL(repoURL, commitSHA, file string, line int) string {
	if commitSHA == "" || file == "" || line <= 0 {
		return ""
	}

	u, err := url.Parse(repoURL)
	if err != nil || !strings.EqualFold(u.Host, githubHost) {
		// Also covers the forms that are not absolute URLs at all: an ssh
		// remote (git@github.com:org/repo.git) fails to parse, and a bare
		// github.com/org/repo parses with no host.
		return ""
	}

	repoPath := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	if len(strings.Split(strings.Trim(repoPath, "/"), "/")) != 2 {
		// Anything but <org>/<repo> is not a repository root, so joining
		// blob/<sha> onto it would produce a confidently wrong link.
		return ""
	}

	// path.Join cleans as it joins, so a file path carrying .. does not climb
	// out of the repository — but it can eat the blob/<sha> segments on the way
	// and leave a plausible GitHub URL that is not a permalink at all. Check
	// the joined path still sits under the prefix rather than trusting it.
	prefix := path.Join(repoPath, "blob", commitSHA)
	joined := path.Join(prefix, file)
	if !strings.HasPrefix(joined, prefix+"/") {
		return ""
	}

	// url.URL.String escapes the path and the fragment.
	link := url.URL{
		Scheme:   "https",
		Host:     githubHost,
		Path:     joined,
		Fragment: "L" + strconv.Itoa(line),
	}

	return link.String()
}

// TopFrames keeps the first n frames and reports whether any were dropped, so
// Code.Frames and Code.FramesTruncated are always set from one call and cannot
// disagree. n <= 0 keeps nothing.
//
// It takes a prefix and does not reorder: Code.Frames is innermost first, but
// that ordering belongs to whichever provider read the stack, so sorting here
// would quietly override it.
//
// The result is clipped rather than a plain subslice. frames[:n] would hold the
// whole backing array alive, which keeps a 200-frame stack in memory to show
// three.
func TopFrames(frames []Frame, n int) (kept []Frame, truncated bool) {
	if n <= 0 {
		return nil, len(frames) > 0
	}
	if len(frames) <= n {
		return frames, false
	}

	return slices.Clip(frames[:n]), true
}
