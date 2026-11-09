package errtrack

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/project-init/devex/internal/sre/errtrack/report"
)

// loadFixture decodes testdata/issue.json, a sanitised copy of a real response.
func loadFixture(t *testing.T) issueResponse {
	t.Helper()

	raw, err := os.ReadFile("testdata/issue.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var resp issueResponse
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&resp); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	return resp
}

func ptr[T any](v T) *T { return &v }

// TestToReportWithoutSourceIntegration is the case the API returns today:
// error_stack is an empty object and there are no git fields, because Source
// Code Integration is not configured. The report still has to be useful, and
// every unavailable field has to be null rather than a plausible zero.
func TestToReportWithoutSourceIntegration(t *testing.T) {
	resp := loadFixture(t)

	got := toReport(resp, mapInput{Site: "datadoghq.com", FetchedAt: testNow})

	t.Run("envelope", func(t *testing.T) {
		if got.SchemaVersion != report.SchemaVersion {
			t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, report.SchemaVersion)
		}
		if got.Source != report.SourceDatadog {
			t.Errorf("Source = %q, want %q", got.Source, report.SourceDatadog)
		}
		if !got.FetchedAt.Equal(testNow) {
			t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, testNow)
		}
	})

	t.Run("summary sits exactly on the cap", func(t *testing.T) {
		const want = "[login-ui-app] *errors.errorString: " +
			"get identity 00000000-0000-0000-0000-000000000000: HTTP 503: undefined response type"
		if got.Summary != want {
			t.Errorf("Summary = %q, want %q", got.Summary, want)
		}
		// 120 runes exactly, so this also pins that a line on the limit is not
		// truncated.
		if n := len([]rune(got.Summary)); n != report.SummaryMaxRunes {
			t.Errorf("Summary is %d runes, want %d", n, report.SummaryMaxRunes)
		}
	})

	t.Run("error", func(t *testing.T) {
		if got.Error.Type != "*errors.errorString" {
			t.Errorf("Type = %q", got.Error.Type)
		}
		if got.Error.Service != "login-ui-app" {
			t.Errorf("Service = %q", got.Error.Service)
		}
		if got.Error.State != "OPEN" {
			t.Errorf("State = %q", got.Error.State)
		}
		if got.Error.Platform != "BACKEND" {
			t.Errorf("Platform = %q", got.Error.Platform)
		}
		if got.Error.IsCrash {
			t.Error("IsCrash = true, want false")
		}

		// The environment comes from the sample's raw event. Datadog's typed
		// attributes have no such field, so this is the only source.
		if got.Error.Environment == nil || *got.Error.Environment != "production" {
			t.Errorf("Environment = %v, want production from the event", got.Error.Environment)
		}
	})

	t.Run("timeline", func(t *testing.T) {
		want := time.Date(2026, 11, 7, 17, 7, 7, 24_000_000, time.UTC)
		if got.Timeline.FirstSeen == nil || !got.Timeline.FirstSeen.Equal(want) {
			t.Errorf("FirstSeen = %v, want %v", got.Timeline.FirstSeen, want)
		}
		if got.Timeline.LastSeen == nil || !got.Timeline.LastSeen.Equal(want) {
			t.Errorf("LastSeen = %v, want %v", got.Timeline.LastSeen, want)
		}
		if got.Timeline.LastSeenVersion != "v1.1.14" {
			t.Errorf("LastSeenVersion = %q, want v1.1.14", got.Timeline.LastSeenVersion)
		}
	})

	// Counts come only from the issue-search endpoint, so a single lookup must
	// report null and never zero.
	t.Run("occurrences stay null", func(t *testing.T) {
		if got.Occurrences.Total != nil || got.Occurrences.ImpactedUsers != nil || got.Occurrences.ImpactedSessions != nil {
			t.Errorf("Occurrences = %+v, want all null", got.Occurrences)
		}
	})

	t.Run("code is empty without source integration", func(t *testing.T) {
		if len(got.Code.Frames) != 0 {
			t.Errorf("Frames = %d, want none", len(got.Code.Frames))
		}
		if got.Code.FramesTruncated {
			t.Error("FramesTruncated = true, want false when there are no frames")
		}
		if got.Code.RepositoryURL != "" || got.Code.CommitSHA != "" {
			t.Errorf("repo/sha = %q/%q, want empty", got.Code.RepositoryURL, got.Code.CommitSHA)
		}

		// The issue's own file_path is the fallback: the path the build saw,
		// container prefix and all, with no line number.
		if got.Code.Location.File != "/app/pkg/kratos/admin.go" {
			t.Errorf("Location.File = %q", got.Code.Location.File)
		}
		if got.Code.Location.Function != "github.com/dailypay/login-ui/pkg/kratos.endSpan" {
			t.Errorf("Location.Function = %q", got.Code.Location.Function)
		}
		if got.Code.Location.Line != nil {
			t.Errorf("Location.Line = %v, want null without a frame", *got.Code.Location.Line)
		}
	})

	t.Run("links", func(t *testing.T) {
		const want = "https://app.datadoghq.com/error-tracking/issue/aae10c1c-dacd-11f1-a39f-da7ad0900002"
		if got.Links.Issue != want {
			t.Errorf("Links.Issue = %q, want %q", got.Links.Issue, want)
		}
		if got.Links.SessionReplay != nil {
			t.Error("SessionReplay should be null for a backend error")
		}
		// A trace link needs Datadog's trace URL format, which has not been
		// confirmed against a real one. No link beats a link that 404s.
		if got.Links.Trace != nil {
			t.Errorf("Links.Trace = %v, want null until the format is confirmed", *got.Links.Trace)
		}
	})
}

// withSourceIntegration is what the same response looks like once Source Code
// Integration is on: git fields on the sample, and error_stack parsed into
// frames. Four frames, so the three-frame cap is exercised, and the third sits
// in a vendored dependency with its own repository and ref.
func withSourceIntegration() issueResponse {
	const (
		serviceRepo = "https://github.com/dailypay/login-ui"
		serviceSHA  = "7c4f1a9e2b8d3c5f6a0e9d8c7b6a5f4e3d2c1b0a"
		vendorSHA   = "1111111111111111111111111111111111111111"
	)

	serviceFrame := func(text, file string, line int) stackFrame {
		return stackFrame{
			Text:            text,
			GitFileLocation: &gitFileLocation{FilePath: file, FirstLine: ptr(line), LastLine: ptr(line)},
		}
	}

	top := serviceFrame("admin.go:118", "pkg/kratos/admin.go", 118)
	top.CodeSnippet = &codeSnippet{Offset: 117, Lines: []string{"a", "b"}}

	return issueResponse{
		Data: issueData{
			ID:   "aae10c1c-dacd-11f1-a39f-da7ad0900002",
			Type: "issue",
			Attributes: issueAttributes{
				ErrorType:    "*errors.errorString",
				ErrorMessage: "boom",
				Service:      "login-ui-app",
				FilePath:     "/app/pkg/kratos/admin.go",
				FunctionName: "pkg/kratos.endSpan",
				FirstSeen:    1794071227024,
				LastSeen:     1794071227024,
			},
		},
		Included: []included{{
			Type: sampleType,
			Attributes: sampleAttributes{
				Biases:           []string{gitMetadataBias},
				GitRepositoryURL: serviceRepo,
				GitCommitSHA:     serviceSHA,
				Event:            sampleEvent{Env: "production"},
				ErrorStack: errorStack{
					Frames: []stackFrame{
						top,
						serviceFrame("admin.go:142", "pkg/kratos/admin.go", 142),
						{
							Text: "vendored",
							GitFileLocation: &gitFileLocation{
								FilePath:  "mux.go",
								FirstLine: ptr(90),
								RepoID:    "github.com/go-chi/chi",
								Ref:       vendorSHA,
							},
						},
						serviceFrame("dropped by the cap", "internal/server/router.go", 214),
					},
				},
			},
		}},
	}
}

func TestToReportWithSourceIntegration(t *testing.T) {
	got := toReport(withSourceIntegration(), mapInput{Site: "datadoghq.com", FetchedAt: testNow})

	t.Run("frame cap is applied and reported", func(t *testing.T) {
		if len(got.Code.Frames) != report.MaxTicketFrames {
			t.Fatalf("Frames = %d, want %d", len(got.Code.Frames), report.MaxTicketFrames)
		}
		if !got.Code.FramesTruncated {
			t.Error("FramesTruncated = false, want true with four frames")
		}
	})

	t.Run("top frame drives the location", func(t *testing.T) {
		// Preferred over the issue's /app-prefixed file_path, and it brings a
		// line number with it.
		if got.Code.Location.File != "pkg/kratos/admin.go" {
			t.Errorf("Location.File = %q, want the frame's repository path", got.Code.Location.File)
		}
		if got.Code.Location.Line == nil || *got.Code.Location.Line != 118 {
			t.Errorf("Location.Line = %v, want 118", got.Code.Location.Line)
		}
	})

	t.Run("service frames link into the service repository", func(t *testing.T) {
		const want = "https://github.com/dailypay/login-ui/blob/" +
			"7c4f1a9e2b8d3c5f6a0e9d8c7b6a5f4e3d2c1b0a/pkg/kratos/admin.go#L118"
		if got.Code.Frames[0].SourceURL != want {
			t.Errorf("SourceURL = %q, want %q", got.Code.Frames[0].SourceURL, want)
		}
		if got.Code.Frames[0].CodeSnippet == nil || got.Code.Frames[0].CodeSnippet.Offset != 117 {
			t.Errorf("CodeSnippet = %+v, want offset 117", got.Code.Frames[0].CodeSnippet)
		}
	})

	// The reason Frame carries its own repository and commit: a frame inside a
	// dependency has to link into that dependency, not into this service.
	t.Run("a vendored frame links into its own repository", func(t *testing.T) {
		vendored := got.Code.Frames[2]
		if vendored.RepositoryURL != "https://github.com/go-chi/chi" {
			t.Errorf("RepositoryURL = %q, want the dependency's", vendored.RepositoryURL)
		}
		if vendored.CommitSHA != "1111111111111111111111111111111111111111" {
			t.Errorf("CommitSHA = %q, want the frame's own ref", vendored.CommitSHA)
		}
		const want = "https://github.com/go-chi/chi/blob/" +
			"1111111111111111111111111111111111111111/mux.go#L90"
		if vendored.SourceURL != want {
			t.Errorf("SourceURL = %q, want %q", vendored.SourceURL, want)
		}
	})

	t.Run("frames without their own repository fall back to the service", func(t *testing.T) {
		if got.Code.Frames[1].RepositoryURL != "https://github.com/dailypay/login-ui" {
			t.Errorf("RepositoryURL = %q, want the service's", got.Code.Frames[1].RepositoryURL)
		}
	})
}

func TestToReportEnvOverride(t *testing.T) {
	t.Run("flag beats the event", func(t *testing.T) {
		got := toReport(loadFixture(t), mapInput{EnvOverride: "staging", FetchedAt: testNow})
		if got.Error.Environment == nil || *got.Error.Environment != "staging" {
			t.Errorf("Environment = %v, want staging", got.Error.Environment)
		}
	})

	t.Run("null when neither supplies one", func(t *testing.T) {
		stripped := loadFixture(t)
		stripped.Included[0].Attributes.Event.Env = ""

		got := toReport(stripped, mapInput{FetchedAt: testNow})
		if got.Error.Environment != nil {
			t.Errorf("Environment = %v, want null", *got.Error.Environment)
		}
	})
}

// TestToReportNoSample covers a response with no sample at all, which is what
// arrives if the bias filter matches nothing. It must produce a report rather
// than panic, since the issue attributes alone are still worth reporting.
func TestToReportNoSample(t *testing.T) {
	resp := loadFixture(t)
	resp.Included = nil

	got := toReport(resp, mapInput{Site: "datadoghq.com", FetchedAt: testNow})

	if got.Error.Type == "" {
		t.Error("lost the issue attributes along with the sample")
	}
	if got.Error.Environment != nil {
		t.Error("Environment should be null with no sample to read it from")
	}
	if got.Links.Issue == "" {
		t.Error("the issue link needs no sample")
	}
}

// TestToReportIsDeterministic guards the purity the package doc claims: same
// input, same bytes, so a golden test downstream is possible at all.
func TestToReportIsDeterministic(t *testing.T) {
	resp := loadFixture(t)
	in := mapInput{Site: "datadoghq.com", FetchedAt: testNow}

	first, err := json.Marshal(toReport(resp, in))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(toReport(resp, in))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("toReport is not deterministic for the same input")
	}
}
