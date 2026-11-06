package report

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
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
		if r.Code.RepositoryURL == "" || r.Code.CommitSHA == "" {
			t.Errorf("frame %d has a source_url but code.repository_url or commit_sha is empty", i)
		}
		if f.File == "" || f.FirstLine == nil {
			t.Errorf("frame %d has a source_url but no file or first_line", i)
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
