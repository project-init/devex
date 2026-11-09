package report

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const examplePath = "testdata/example.json"

// TestExample checks the documented example and the structs agree, and that the
// example still documents the awkward cases rather than the easy ones.
func TestExample(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	// DisallowUnknownFields is the point: rename a json tag and the key in the
	// example no longer has a field to land in, so this fails rather than
	// silently dropping data.
	var r Report
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("strict decode %s: %v", examplePath, err)
	}

	t.Run("round trips", func(t *testing.T) {
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		// Compared as documents, not bytes: prettier formats JSON here and
		// keeps short arrays on one line where MarshalIndent expands them.
		var want, got any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("round trip changed the document\n file: %s\n got:  %s", raw, out)
		}
	})

	// Filling every field in would make the example tidier and stop it earning
	// its keep, so these fail if someone does.
	t.Run("documents the null cases", func(t *testing.T) {
		if r.Links.SessionReplay != nil {
			t.Error("session_replay should be null: a backend error has no replay")
		}
		if n := len(r.Code.Frames); n == 0 {
			t.Fatal("code.frames is empty")
		}
		if r.Code.Frames[len(r.Code.Frames)-1].CodeSnippet != nil {
			t.Error("the last frame should have a null code_snippet: Datadog resolves source only when it can")
		}
		if len(r.Code.Frames) != MaxTicketFrames || !r.Code.FramesTruncated {
			t.Errorf("frames = %d, truncated = %v; want %d and true so the cap is visible",
				len(r.Code.Frames), r.Code.FramesTruncated, MaxTicketFrames)
		}
	})

	t.Run("honours the schema's own rules", func(t *testing.T) {
		if r.SchemaVersion != SchemaVersion {
			t.Errorf("schema_version = %d, want %d", r.SchemaVersion, SchemaVersion)
		}
		if n := len([]rune(r.Summary)); n > SummaryMaxRunes {
			t.Errorf("summary is %d runes, over the %d cap", n, SummaryMaxRunes)
		}

		// A frame claiming a source_url must carry the parts that build one,
		// and the url must pin a commit: a branch link rots as the file moves.
		for i, f := range r.Code.Frames {
			if f.SourceURL == "" {
				continue
			}
			if f.RepositoryURL == "" || f.CommitSHA == "" || f.File == "" || f.FirstLine == nil {
				t.Errorf("frame %d has a source_url but not the parts that build one", i)
			}
			if !strings.Contains(f.SourceURL, f.CommitSHA) {
				t.Errorf("frame %d source_url does not pin its commit: %s", i, f.SourceURL)
			}
		}
	})
}

// TestNoOmitEmpty guards the "every key is always present" convention by reading
// the struct tags, recursively.
//
// Checking the marshalled output would miss most of it: encoding/json ignores
// omitempty on struct values, so a stray omitempty on error, timeline,
// occurrences, code or links changes nothing observable today — and starts
// dropping keys the moment one of those becomes a pointer.
func TestNoOmitEmpty(t *testing.T) {
	var offenders []string
	seen := map[reflect.Type]bool{}

	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		// time.Time marshals through its own MarshalJSON, so its unexported
		// fields are not part of this schema.
		if typ.Kind() != reflect.Struct || seen[typ] || typ.PkgPath() == "time" {
			return
		}
		seen[typ] = true

		for i := range typ.NumField() {
			f := typ.Field(i)
			name := path + "." + f.Name
			if slices := strings.Split(f.Tag.Get("json"), ","); len(slices) > 1 {
				for _, opt := range slices[1:] {
					if opt == "omitempty" {
						offenders = append(offenders, name)
					}
				}
			}
			walk(f.Type, name)
		}
	}
	walk(reflect.TypeOf(Report{}), "Report")

	if len(offenders) > 0 {
		t.Errorf("omitempty breaks the always-present convention on: %s", strings.Join(offenders, ", "))
	}
}

func TestSummarize(t *testing.T) {
	// Multi-byte throughout, so every cut position lands mid-rune.
	wide := strings.Repeat("占", 200)

	tests := map[string]struct {
		service, errType, message string
		want                      string // exact result, when short enough to state
	}{
		"short line is returned whole": {
			service: "gigapi", errType: "runtime.Error", message: "boom",
			want: "[gigapi] runtime.Error: boom",
		},
		"empty message still produces the prefix": {
			service: "gigapi", errType: "runtime.Error",
			want: "[gigapi] runtime.Error: ",
		},
		"long message is truncated": {
			service: "gigapi", errType: "runtime.Error", message: strings.Repeat("overflow ", 30),
		},
		"multi-byte message survives truncation": {
			service: "gigapi", errType: "runtime.Error", message: wide,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := Summarize(tt.service, tt.errType, tt.message)

			if tt.want != "" {
				if got != tt.want {
					t.Errorf("Summarize() = %q, want %q", got, tt.want)
				}

				return
			}

			if n := len([]rune(got)); n != SummaryMaxRunes {
				t.Errorf("Summarize() is %d runes, want exactly %d: %q", n, SummaryMaxRunes, got)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("truncated summary should end in an ellipsis: %q", got)
			}
			// Byte slicing would cut a rune in half and emit invalid UTF-8
			// into the document.
			if !utf8.ValidString(got) {
				t.Errorf("Summarize() produced invalid UTF-8: %q", got)
			}
		})
	}

	// A line exactly on the cap must not be touched. Built rather than
	// hardcoded so it stays correct if the cap or the format changes.
	t.Run("a line exactly on the cap is untouched", func(t *testing.T) {
		const service, errType = "gigapi", "runtime.Error"
		prefix := len([]rune(Summarize(service, errType, "")))

		got := Summarize(service, errType, strings.Repeat("x", SummaryMaxRunes-prefix))
		if strings.HasSuffix(got, "…") {
			t.Errorf("a line on the cap should not be truncated: %q", got)
		}
		if n := len([]rune(got)); n != SummaryMaxRunes {
			t.Errorf("setup produced %d runes, want %d", n, SummaryMaxRunes)
		}
	})
}

func TestSourceURL(t *testing.T) {
	const (
		repo = "https://github.com/dailypay/gigapi"
		sha  = "7c4f1a9e2b8d3c5f6a0e9d8c7b6a5f4e3d2c1b0a"
		file = "internal/transfers/service.go"
		want = repo + "/blob/" + sha + "/" + file + "#L142"
	)

	tests := map[string]struct {
		repo, sha, file string
		line            int
		want            string
	}{
		"github permalink": {repo: repo, sha: sha, file: file, line: 142, want: want},

		// Forms a git remote can carry, which must normalise rather than fail.
		"git suffix and trailing slash trimmed": {repo: repo + ".git/", sha: sha, file: file, line: 142, want: want},
		"host case ignored":                     {repo: "https://GitHub.com/dailypay/gigapi", sha: sha, file: file, line: 142, want: want},

		// Another forge puts blob at a different path and uses a different
		// line anchor, so a GitHub-shaped URL 404s. No link beats a dead link.
		"other forge is not built": {repo: "https://gitlab.com/dailypay/gigapi", sha: sha, file: file, line: 142},

		// Not a repository root, so blob/<sha> would hang off the wrong path.
		"not a repository root": {repo: "https://github.com/dailypay", sha: sha, file: file, line: 142},

		// A permalink must pin a commit and a line.
		"no commit": {repo: repo, file: file, line: 142},
		"no line":   {repo: repo, sha: sha, file: file},

		// path.Join eats blob/<sha> on the way through, leaving a plausible
		// GitHub URL that is not a permalink at all.
		"path traversal": {repo: repo, sha: sha, file: "../../etc/passwd", line: 1},

		"space in filename is escaped": {
			repo: repo, sha: sha, file: "pkg/my file.go", line: 7,
			want: repo + "/blob/" + sha + "/pkg/my%20file.go#L7",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := SourceURL(tt.repo, tt.sha, tt.file, tt.line); got != tt.want {
				t.Errorf("SourceURL(%q, %q, %q, %d)\n got  %q\n want %q",
					tt.repo, tt.sha, tt.file, tt.line, got, tt.want)
			}
		})
	}
}

func TestTopFrames(t *testing.T) {
	named := func(n int) []Frame {
		out := make([]Frame, n)
		for i := range out {
			out[i] = Frame{File: string(rune('a' + i))}
		}

		return out
	}
	files := func(frames []Frame) string {
		var b strings.Builder
		for _, f := range frames {
			b.WriteString(f.File)
		}

		return b.String()
	}

	tests := map[string]struct {
		frames        []Frame
		n             int
		wantFiles     string
		wantTruncated bool
	}{
		"nil frames":         {n: 3},
		"fewer than the cap": {frames: named(2), n: 3, wantFiles: "ab"},

		// The off-by-one that matters: a stack exactly on the cap is whole, so
		// reporting it truncated would mark a complete trace incomplete.
		"exactly the cap": {frames: named(3), n: 3, wantFiles: "abc"},

		// Named by index, so this also catches returning the last n rather
		// than the first n, which keeps the count right.
		"over the cap": {frames: named(5), n: 3, wantFiles: "abc", wantTruncated: true},
		"zero cap":     {frames: named(3), n: 0, wantTruncated: true},
		"negative cap": {frames: named(3), n: -1, wantTruncated: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			kept, truncated := TopFrames(tt.frames, tt.n)

			if got := files(kept); got != tt.wantFiles {
				t.Errorf("kept = %q, want %q", got, tt.wantFiles)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
		})
	}
}
