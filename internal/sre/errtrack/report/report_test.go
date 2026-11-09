package report

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const examplePath = "testdata/example.json"

// decodeExample reads testdata/example.json strictly. DisallowUnknownFields is
// the point of the strict decode: rename a json tag and the key in the example
// no longer has a field to land in, so the decode fails instead of silently
// dropping data.
func decodeExample(t *testing.T) ([]byte, Report) {
	t.Helper()

	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	var r Report
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("strict decode %s: %v", examplePath, err)
	}

	return raw, r
}

// TestExampleRoundTrips checks the documented example and the structs agree.
//
// The comparison is semantic rather than byte-for-byte on purpose: prettier
// formats JSON in this repository and keeps short arrays on one line, while
// json.MarshalIndent expands them. Byte equality would make the test and the
// formatter permanently disagree, and whitespace is not what this test is for.
func TestExampleRoundTrips(t *testing.T) {
	raw, r := decodeExample(t)

	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("unmarshal example: %v", err)
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}

	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip changed the document\n file: %s\n got:  %s", raw, out)
	}
}

// TestExampleCoversNullCases asserts the example still documents the awkward
// cases rather than the easy ones. Filling every field in would make the
// example tidier and stop it earning its keep, so these assertions fail if
// someone does.
func TestExampleCoversNullCases(t *testing.T) {
	_, r := decodeExample(t)

	t.Run("populated fields are set", func(t *testing.T) {
		if r.Error.Environment == nil {
			t.Error("error.environment is null; the example should show an --env value")
		}
		if r.Timeline.FirstSeen == nil || r.Timeline.LastSeen == nil {
			t.Error("timeline timestamps are null; the example should show both set")
		}
		if r.Occurrences.Total == nil {
			t.Error("occurrences.total is null; the example should show counts resolved")
		}
	})

	t.Run("null cases stay null", func(t *testing.T) {
		// Backend errors have no session replay.
		if r.Links.SessionReplay != nil {
			t.Error("links.session_replay should be null")
		}
		// Datadog resolves source only when it can, so a renderer has to cope
		// with a frame that has no snippet.
		if n := len(r.Code.Frames); n == 0 {
			t.Fatal("code.frames is empty")
		}
		if r.Code.Frames[len(r.Code.Frames)-1].CodeSnippet != nil {
			t.Error("the last frame should have a null code_snippet")
		}
	})

	t.Run("frame cap is exercised", func(t *testing.T) {
		if got := len(r.Code.Frames); got != MaxTicketFrames {
			t.Errorf("code.frames = %d, want %d", got, MaxTicketFrames)
		}
		if !r.Code.FramesTruncated {
			t.Error("code.frames_truncated should be true so the cap is visible")
		}
	})
}

// TestExampleInvariants covers the rules the schema promises, independent of
// which example happens to be committed.
func TestExampleInvariants(t *testing.T) {
	_, r := decodeExample(t)

	if r.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", r.SchemaVersion, SchemaVersion)
	}
	if r.Source != SourceDatadog {
		t.Errorf("source = %q, want %q", r.Source, SourceDatadog)
	}
	if r.FetchedAt.IsZero() {
		t.Error("fetched_at is zero; the tool always sets it")
	}

	// Counted in runes, because Summarize truncates in runes.
	if n := len([]rune(r.Summary)); n > SummaryMaxRunes {
		t.Errorf("summary is %d runes, over the %d cap", n, SummaryMaxRunes)
	}

	// source_url is derived from repository_url, commit_sha, file and line, so
	// a frame that claims a URL must carry the parts that build one.
	for i, f := range r.Code.Frames {
		if f.SourceURL == "" {
			continue
		}
		// The frame's own repository and commit, not the service's: SourceURL
		// is built from the frame, so a frame in a vendored dependency links
		// into that repository rather than this service's.
		if f.RepositoryURL == "" || f.CommitSHA == "" {
			t.Errorf("frame %d has a source_url but its own repository_url or commit_sha is empty", i)
		}
		if f.File == "" || f.FirstLine == nil {
			t.Errorf("frame %d has a source_url but no file or first_line", i)
		}
		// A permalink has to pin a commit; a branch link rots as the file moves.
		if !strings.Contains(f.SourceURL, f.CommitSHA) {
			t.Errorf("frame %d source_url does not pin its commit: %s", i, f.SourceURL)
		}
	}
}

// TestNoOmitEmpty guards the "every key is always present" convention by
// reading the struct tags directly, recursively.
//
// Checking the marshalled output instead would miss most of it: encoding/json
// ignores omitempty on struct values, so a stray omitempty on error, timeline,
// occurrences, code or links changes nothing observable today — and then starts
// dropping keys the moment one of those becomes a pointer.
func TestNoOmitEmpty(t *testing.T) {
	var offenders []string

	var walk func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true

		// time.Time marshals through its own MarshalJSON, so its unexported
		// fields are not part of our schema.
		if typ == reflect.TypeOf(time.Time{}) {
			return
		}

		for i := range typ.NumField() {
			f := typ.Field(i)
			name := path + "." + f.Name
			for _, opt := range strings.Split(f.Tag.Get("json"), ",")[1:] {
				if opt == "omitempty" {
					offenders = append(offenders, name)
				}
			}
			walk(f.Type, name)
		}
	}
	walk(reflect.TypeOf(Report{}), "Report")

	if len(offenders) > 0 {
		t.Errorf("omitempty breaks the always-present convention on: %s",
			strings.Join(offenders, ", "))
	}
}

// TestTopLevelKeysAlwaysPresent checks the marshalled output of a zero Report,
// so the convention is verified in the document and not only in the tags.
func TestTopLevelKeysAlwaysPresent(t *testing.T) {
	out, err := json.Marshal(Report{})
	if err != nil {
		t.Fatalf("marshal zero report: %v", err)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(out, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, want := range []string{
		"schema_version", "source", "fetched_at", "summary",
		"error", "timeline", "occurrences", "code", "links",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("zero Report omits %q; check for a stray omitempty", want)
		}
	}
}

func TestSummarize(t *testing.T) {
	// A message long enough to push the line past the cap on its own.
	longMessage := strings.Repeat("overflow ", 30)

	// Multi-byte throughout, so a byte-wise cut lands mid-rune.
	wideMessage := strings.Repeat("占", 200)

	tests := []struct {
		name    string
		service string
		errType string
		message string
		want    string // exact result, when short enough to state
	}{
		{
			name:    "short line is returned whole",
			service: "gigapi",
			errType: "runtime.Error",
			message: "boom",
			want:    "[gigapi] runtime.Error: boom",
		},
		{
			name:    "empty message still produces the prefix",
			service: "gigapi",
			errType: "runtime.Error",
			want:    "[gigapi] runtime.Error: ",
		},
		{name: "long message is truncated", service: "gigapi", errType: "runtime.Error", message: longMessage},
		{name: "multi-byte message is truncated", service: "gigapi", errType: "runtime.Error", message: wideMessage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Summarize(tt.service, tt.errType, tt.message)

			if tt.want != "" {
				if got != tt.want {
					t.Errorf("Summarize() = %q, want %q", got, tt.want)
				}

				return
			}

			// Truncating cases: within the cap, marked, and still valid UTF-8.
			if n := len([]rune(got)); n > SummaryMaxRunes {
				t.Errorf("Summarize() is %d runes, over the %d cap: %q", n, SummaryMaxRunes, got)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("truncated summary should end in an ellipsis, got %q", got)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Summarize() produced invalid UTF-8: %q", got)
			}
		})
	}

	// The boundary: a line exactly at the cap must not be touched. Built by
	// padding the message until the whole line is SummaryMaxRunes long.
	t.Run("line exactly at the cap is untouched", func(t *testing.T) {
		const service, errType = "gigapi", "runtime.Error"
		prefix := len([]rune(Summarize(service, errType, "")))
		message := strings.Repeat("x", SummaryMaxRunes-prefix)

		got := Summarize(service, errType, message)
		if n := len([]rune(got)); n != SummaryMaxRunes {
			t.Fatalf("test setup produced %d runes, want exactly %d", n, SummaryMaxRunes)
		}
		if strings.HasSuffix(got, "…") {
			t.Errorf("a line exactly at the cap should not be truncated, got %q", got)
		}
	})

	// One rune over the cap is the off-by-one case: it must truncate, and the
	// result must still fit.
	t.Run("one rune over the cap truncates", func(t *testing.T) {
		const service, errType = "gigapi", "runtime.Error"
		prefix := len([]rune(Summarize(service, errType, "")))
		message := strings.Repeat("x", SummaryMaxRunes-prefix+1)

		got := Summarize(service, errType, message)
		if n := len([]rune(got)); n != SummaryMaxRunes {
			t.Errorf("Summarize() = %d runes, want exactly %d: %q", n, SummaryMaxRunes, got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("expected an ellipsis, got %q", got)
		}
	})
}

func TestSourceURL(t *testing.T) {
	const (
		repo = "https://github.com/dailypay/gigapi"
		sha  = "7c4f1a9e2b8d3c5f6a0e9d8c7b6a5f4e3d2c1b0a"
		file = "internal/transfers/service.go"
	)
	const want = "https://github.com/dailypay/gigapi/blob/" + sha + "/" + file + "#L142"

	tests := []struct {
		name            string
		repo, sha, file string
		line            int
		want            string
	}{
		{name: "github permalink", repo: repo, sha: sha, file: file, line: 142, want: want},

		// Forms a git remote can carry. Each must normalise, not be rejected.
		{name: "git suffix trimmed", repo: repo + ".git", sha: sha, file: file, line: 142, want: want},
		{name: "trailing slash trimmed", repo: repo + "/", sha: sha, file: file, line: 142, want: want},
		{name: "host case ignored", repo: "https://GitHub.com/dailypay/gigapi", sha: sha, file: file, line: 142, want: want},

		// Other forges: the path segment and line anchor both differ, so a
		// GitHub-shaped URL would 404. No link beats a dead link.
		{name: "gitlab is not built", repo: "https://gitlab.com/dailypay/gigapi", sha: sha, file: file, line: 142},
		{name: "bitbucket is not built", repo: "https://bitbucket.org/dailypay/gigapi", sha: sha, file: file, line: 142},

		// Not absolute URLs at all.
		{name: "ssh remote", repo: "git@github.com:dailypay/gigapi.git", sha: sha, file: file, line: 142},
		{name: "no scheme", repo: "github.com/dailypay/gigapi", sha: sha, file: file, line: 142},
		{name: "empty repo", sha: sha, file: file, line: 142},
		{name: "not a url", repo: "not a url at all", sha: sha, file: file, line: 142},

		// Not a repository root, so blob/<sha> would hang off the wrong path.
		{name: "host only", repo: "https://github.com", sha: sha, file: file, line: 142},
		{name: "org only", repo: "https://github.com/dailypay", sha: sha, file: file, line: 142},
		{name: "too many segments", repo: "https://github.com/dailypay/gigapi/tree/main", sha: sha, file: file, line: 142},

		// Missing parts. A permalink must pin a commit and a line.
		{name: "no commit", repo: repo, file: file, line: 142},
		{name: "no file", repo: repo, sha: sha, line: 142},
		{name: "line zero", repo: repo, sha: sha, file: file},
		{name: "negative line", repo: repo, sha: sha, file: file, line: -1},

		// A traversing path survives path.Join by eating blob/<sha>, which
		// would leave a plausible GitHub URL that is not a permalink.
		{name: "path traversal", repo: repo, sha: sha, file: "../../etc/passwd", line: 1},

		// Escaping, not rejection: a space is legal in a filename.
		{
			name: "space in filename escaped",
			repo: repo, sha: sha, file: "pkg/my file.go", line: 7,
			want: "https://github.com/dailypay/gigapi/blob/" + sha + "/pkg/my%20file.go#L7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SourceURL(tt.repo, tt.sha, tt.file, tt.line)
			if got != tt.want {
				t.Errorf("SourceURL(%q, %q, %q, %d)\n got  %q\n want %q",
					tt.repo, tt.sha, tt.file, tt.line, got, tt.want)
			}
		})
	}
}

// framesNamed builds frames whose File is its index, so a test can assert which
// frames came back rather than only how many. Returning the last n instead of
// the first n keeps the count right and would otherwise pass.
func framesNamed(n int) []Frame {
	out := make([]Frame, n)
	for i := range out {
		out[i] = Frame{File: strconv.Itoa(i)}
	}

	return out
}

func filesOf(frames []Frame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.File)
	}

	return out
}

func TestTopFrames(t *testing.T) {
	tests := []struct {
		name          string
		frames        []Frame
		n             int
		wantFiles     []string
		wantTruncated bool
	}{
		{name: "nil frames", frames: nil, n: MaxTicketFrames, wantFiles: []string{}},
		{name: "no frames", frames: framesNamed(0), n: MaxTicketFrames, wantFiles: []string{}},
		{name: "fewer than the cap", frames: framesNamed(2), n: 3, wantFiles: []string{"0", "1"}},

		// The off-by-one that matters: a stack exactly at the cap is whole, so
		// reporting it as truncated would mark a complete trace incomplete.
		{name: "exactly the cap", frames: framesNamed(3), n: 3, wantFiles: []string{"0", "1", "2"}},

		{name: "one over the cap", frames: framesNamed(4), n: 3, wantFiles: []string{"0", "1", "2"}, wantTruncated: true},
		{name: "far over the cap", frames: framesNamed(200), n: 3, wantFiles: []string{"0", "1", "2"}, wantTruncated: true},
		{name: "cap of one", frames: framesNamed(5), n: 1, wantFiles: []string{"0"}, wantTruncated: true},

		// n <= 0 keeps nothing, and says so when there was something to keep.
		{name: "zero cap with frames", frames: framesNamed(3), n: 0, wantFiles: []string{}, wantTruncated: true},
		{name: "zero cap without frames", frames: nil, n: 0, wantFiles: []string{}},
		{name: "negative cap", frames: framesNamed(3), n: -1, wantFiles: []string{}, wantTruncated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, truncated := TopFrames(tt.frames, tt.n)

			if got := filesOf(kept); !reflect.DeepEqual(got, tt.wantFiles) {
				t.Errorf("kept frames = %v, want %v", got, tt.wantFiles)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
		})
	}
}

// TestTopFramesDoesNotAliasTheInput checks the clip: writing to the result must
// not reach back into the caller's slice, and the result must not hold the
// whole backing array alive.
func TestTopFramesDoesNotAliasTheInput(t *testing.T) {
	in := framesNamed(10)

	kept, truncated := TopFrames(in, 3)
	if !truncated {
		t.Fatal("expected truncated for 10 frames capped at 3")
	}
	if cap(kept) != len(kept) {
		t.Errorf("cap(kept) = %d, len = %d: the result still holds the full backing array",
			cap(kept), len(kept))
	}

	// Appending to a clipped slice reallocates, so the caller's frame 3 stays.
	kept = append(kept, Frame{File: "clobber"})
	if in[3].File != "3" {
		t.Errorf("appending to the result overwrote the input: in[3].File = %q", in[3].File)
	}
	_ = kept
}

// TestSourceURLMatchesExample ties the link format to the committed example the
// same way TestSummarizeMatchesExample does for the summary: each frame's
// source_url must be exactly what SourceURL builds from the fields beside it.
func TestSourceURLMatchesExample(t *testing.T) {
	_, r := decodeExample(t)

	for i, f := range r.Code.Frames {
		if f.FirstLine == nil {
			continue
		}
		got := SourceURL(f.RepositoryURL, f.CommitSHA, f.File, *f.FirstLine)
		if got != f.SourceURL {
			t.Errorf("frame %d:\n got  %q\n want %q", i, got, f.SourceURL)
		}
	}
}

// TestSummarizeMatchesExample ties the format to the committed example: the
// summary in testdata/example.json must be exactly what Summarize builds from
// the error fields beside it. Without this, the function and the documented
// schema can drift apart silently — the round-trip test only compares the file
// against the structs, not against this function.
func TestSummarizeMatchesExample(t *testing.T) {
	_, r := decodeExample(t)

	got := Summarize(r.Error.Service, r.Error.Type, r.Error.Message)
	if got != r.Summary {
		t.Errorf("Summarize() = %q\nexample summary = %q", got, r.Summary)
	}
}
