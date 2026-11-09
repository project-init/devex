package errtrack

import (
	"fmt"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/errtrack/report"
)

// mapInput is everything toReport needs that does not come from Datadog.
type mapInput struct {
	// Site is the Datadog site the issue was read from, used to rebuild the
	// link back to it.
	Site string

	// EnvOverride is --env. The sample's own event carries an environment, so
	// this only applies when the caller insists.
	EnvOverride string

	// FetchedAt is injected rather than read from the clock, so the output of
	// a run is a function of its input.
	FetchedAt time.Time
}

// toReport turns a Datadog issue into the document errtrack emits.
//
// Pure: no clock, no network, no environment. That is what makes it testable
// against a saved response, which is the only part of the fetch path that can
// be covered without talking to Datadog.
//
// Occurrences are left null throughout. Datadog serves counts only from the
// issue-search endpoint, never from a single issue lookup, so reporting zero
// would be a lie.
func toReport(resp issueResponse, in mapInput) report.Report {
	attrs := resp.Data.Attributes
	sample, _ := resp.sample()

	rep := report.Report{
		SchemaVersion: report.SchemaVersion,
		Source:        report.SourceDatadog,
		FetchedAt:     in.FetchedAt,
		Summary:       report.Summarize(attrs.Service, attrs.ErrorType, attrs.ErrorMessage),

		Error: report.Failure{
			Type:        attrs.ErrorType,
			Message:     attrs.ErrorMessage,
			Service:     attrs.Service,
			Environment: environment(sample, in.EnvOverride),
			State:       attrs.State,
			IsCrash:     attrs.IsCrash,
			Platform:    attrs.Platform,
			Languages:   attrs.Languages,
		},

		Timeline: report.Timeline{
			FirstSeen:        millisToTime(attrs.FirstSeen),
			FirstSeenVersion: attrs.FirstSeenVersion,
			LastSeen:         millisToTime(attrs.LastSeen),
			LastSeenVersion:  attrs.LastSeenVersion,
		},

		Code:  codeOf(attrs, sample),
		Links: linksOf(resp.Data.ID, in.Site, sample),
	}

	return rep
}

// environment prefers --env, then the sample's event. Datadog's typed issue and
// sample attributes carry no environment at all, so without the raw event there
// is nothing to report.
func environment(sample sampleAttributes, override string) *string {
	for _, candidate := range []string{override, sample.Event.Env} {
		if candidate != "" {
			return &candidate
		}
	}

	return nil
}

// millisToTime converts Datadog's epoch milliseconds, returning nil for an
// absent value rather than 1970.
func millisToTime(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()

	return &t
}

// codeOf locates the error in the source tree.
//
// Everything here depends on Source Code Integration: without it the sample
// has no git fields and error_stack is an empty object, so the frames are empty
// and no source link can be built. The location still falls back to the
// issue's own file and function, which is all Datadog gives in that case.
func codeOf(attrs issueAttributes, sample sampleAttributes) report.Code {
	frames := framesOf(sample)
	kept, truncated := report.TopFrames(frames, report.MaxTicketFrames)

	code := report.Code{
		RepositoryURL:   sample.GitRepositoryURL,
		CommitSHA:       sample.GitCommitSHA,
		Location:        locationOf(attrs, kept),
		Frames:          kept,
		FramesTruncated: truncated,
	}

	return code
}

// locationOf is the single place a ticket leads with.
//
// The top frame is preferred when there is one: it is repository-relative and
// carries a line number. The issue's own file_path is the fallback, and it is
// the path the build saw — often prefixed with a container directory — with no
// line at all.
func locationOf(attrs issueAttributes, frames []report.Frame) report.Location {
	loc := report.Location{
		File:     attrs.FilePath,
		Function: attrs.FunctionName,
	}

	if len(frames) > 0 && frames[0].File != "" {
		loc.File = frames[0].File
		loc.Line = frames[0].FirstLine
	}

	return loc
}

// framesOf maps the parsed stack, innermost first, as Datadog returns it.
func framesOf(sample sampleAttributes) []report.Frame {
	if len(sample.ErrorStack.Frames) == 0 {
		return nil
	}

	frames := make([]report.Frame, 0, len(sample.ErrorStack.Frames))
	for _, f := range sample.ErrorStack.Frames {
		frames = append(frames, frameOf(f, sample))
	}

	return frames
}

// frameOf maps one frame, resolving its repository and ref before building the
// permalink.
//
// A frame's own git_file_location wins over the sample's fields: Datadog
// resolves both per frame, so a frame inside a vendored dependency links into
// that dependency rather than into this service. The sample's values are the
// fallback, so a consumer reads one populated pair either way.
func frameOf(f stackFrame, sample sampleAttributes) report.Frame {
	frame := report.Frame{
		Text:          f.Text,
		RepositoryURL: sample.GitRepositoryURL,
		CommitSHA:     sample.GitCommitSHA,
	}

	if loc := f.GitFileLocation; loc != nil {
		frame.File = loc.FilePath
		frame.FirstLine = loc.FirstLine
		frame.LastLine = loc.LastLine

		if loc.RepoID != "" {
			// repo_id has no scheme, e.g. github.com/org/repo.
			frame.RepositoryURL = "https://" + loc.RepoID
		}
		if loc.Ref != "" {
			frame.CommitSHA = loc.Ref
		}
	}

	if s := f.CodeSnippet; s != nil {
		frame.CodeSnippet = &report.CodeSnippet{Offset: s.Offset, Lines: s.Lines}
	}

	if frame.FirstLine != nil {
		frame.SourceURL = report.SourceURL(frame.RepositoryURL, frame.CommitSHA, frame.File, *frame.FirstLine)
	}

	return frame
}

// linksOf points back at Datadog.
//
// Only the issue link is built. A trace deep link would need Datadog's trace
// URL format, which is not documented in the API spec and has not been checked
// against a real one — and by the same rule SourceURL follows, a link that may
// 404 is worse on a ticket than no link. The trace and span ids are in the
// response if someone wants to add it after confirming the format.
func linksOf(issueID, site string, sample sampleAttributes) report.Links {
	links := report.Links{}

	if issueID != "" && site != "" {
		links.Issue = fmt.Sprintf("https://app.%s/error-tracking/issue/%s", site, issueID)
	}
	if url := strings.TrimSpace(sample.SessionReplayURL); url != "" {
		links.SessionReplay = &url
	}

	return links
}
