package errtrack

import "time"

// These are Datadog's wire shapes, kept apart from the report package on
// purpose: these change when Datadog changes, the report changes when we decide
// it should. Only the fields a report needs are declared — IssueSampleAttributes
// alone has 27 — and encoding/json drops the rest.
//
// The payload is JSON:API shaped: data holds the issue, and included holds the
// related resources asked for with include=samples, matched back by type.

// sampleType is the included resource type for an error sample. Note it is not
// "issue_sample": the real response says error_tracking_issue_sample.
const sampleType = "error_tracking_issue_sample"

// gitMetadataBias is the only sample bias whose error_stack carries parsed
// frames, per the API spec. Requesting any other bias returns a sample with no
// stack trace at all.
const gitMetadataBias = "git_metadata"

// issueResponse is GET /api/v2/error-tracking/issues/{issue_id}.
type issueResponse struct {
	Data     issueData  `json:"data"`
	Included []included `json:"included"`
}

type issueData struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Attributes issueAttributes `json:"attributes"`
}

// issueAttributes is the grouped issue. It carries no environment and no line
// number; both come from a sample.
type issueAttributes struct {
	ErrorType    string `json:"error_type"`
	ErrorMessage string `json:"error_message"`
	Service      string `json:"service"`
	State        string `json:"state"`
	Platform     string `json:"platform"`
	IsCrash      bool   `json:"is_crash"`

	Languages []string `json:"languages"`

	// FilePath is where the error was recorded, as the build saw it, so it can
	// carry a container prefix such as /app rather than a repository path.
	// A frame's git_file_location is repository-relative and preferred.
	FilePath     string `json:"file_path"`
	FunctionName string `json:"function_name"`

	// FirstSeen and LastSeen are epoch milliseconds. A sample's timestamp is
	// RFC 3339 instead, so the two cannot be decoded the same way.
	FirstSeen int64 `json:"first_seen"`
	LastSeen  int64 `json:"last_seen"`

	// The versions are whatever the service tags builds with, which can be a
	// release tag such as v1.1.14 rather than a commit.
	FirstSeenVersion string `json:"first_seen_version"`
	LastSeenVersion  string `json:"last_seen_version"`
}

// included is one related resource. Only samples are requested, so anything
// else is skipped by type.
type included struct {
	Type       string           `json:"type"`
	ID         string           `json:"id"`
	Attributes sampleAttributes `json:"attributes"`
}

// sampleAttributes is one error occurrence picked out of the group.
type sampleAttributes struct {
	Biases     []string `json:"biases"`
	DataSource string   `json:"data_source"`
	Language   string   `json:"language"`
	Service    string   `json:"service"`
	Version    string   `json:"version"`

	// TraceID here is the decimal low half. The raw event carries the full hex
	// id, which is what a Datadog trace link needs.
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`

	Timestamp time.Time `json:"timestamp"`

	// GitCommitSHA and GitRepositoryURL arrive only when Source Code
	// Integration is configured for the service. Without it they are absent,
	// error_stack is an empty object, and no source link can be built.
	GitCommitSHA     string `json:"git_commit_sha"`
	GitRepositoryURL string `json:"git_repository_url"`
	SessionReplayURL string `json:"session_replay_url"`

	ErrorStack errorStack  `json:"error_stack"`
	Event      sampleEvent `json:"event"`
}

// hasBias reports whether this sample was picked by the named bias.
func (s sampleAttributes) hasBias(bias string) bool {
	for _, b := range s.Biases {
		if b == bias {
			return true
		}
	}

	return false
}

type errorStack struct {
	Language string       `json:"language"`
	Frames   []stackFrame `json:"frames"`
}

type stackFrame struct {
	Text            string           `json:"text"`
	CodeSnippet     *codeSnippet     `json:"code_snippet"`
	GitFileLocation *gitFileLocation `json:"git_file_location"`
}

type codeSnippet struct {
	Lines  []string `json:"lines"`
	Offset int      `json:"offset"`
}

// gitFileLocation is resolved per frame, so a frame in a vendored dependency
// reports that dependency's repository and ref rather than the service's.
type gitFileLocation struct {
	FilePath  string `json:"filepath"`
	FirstLine *int   `json:"first_line"`
	LastLine  *int   `json:"last_line"`
	Column    *int   `json:"column"`
	Ref       string `json:"ref"`

	// RepoID is host and path with no scheme, e.g. github.com/org/repo.
	RepoID string `json:"repo_id"`
}

// sampleEvent is the raw APM event behind the sample. It holds things the typed
// attributes leave out — the environment and the endpoint among them — so only
// the fields a report reads are declared.
type sampleEvent struct {
	Env          string `json:"env"`
	ResourceName string `json:"resource_name"`

	// TraceID is the full hex id, unlike the sample's decimal one.
	TraceID string `json:"trace_id"`

	Root struct {
		ResourceName string `json:"resource_name"`
		Service      string `json:"service"`
	} `json:"root"`

	Error struct {
		// Stack is the throwing stack and HandlingStack is where the error was
		// recorded. dd-trace-go moved the handling stack to its own field in
		// v2.7.0, so a service on a newer tracer populates HandlingStack and
		// may leave Stack empty. Neither is parsed into frames.
		Stack         string `json:"stack"`
		HandlingStack string `json:"handling_stack"`
	} `json:"error"`
}

// sample returns the sample a report should be built from: the one picked by
// the git_metadata bias, since it is the only one that can carry frames,
// falling back to any sample so an issue without source integration still
// yields an environment and a trace.
func (r issueResponse) sample() (sampleAttributes, bool) {
	var fallback sampleAttributes
	found := false

	for _, inc := range r.Included {
		if inc.Type != sampleType {
			continue
		}
		if inc.Attributes.hasBias(gitMetadataBias) {
			return inc.Attributes, true
		}
		if !found {
			fallback, found = inc.Attributes, true
		}
	}

	return fallback, found
}
