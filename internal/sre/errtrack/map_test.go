package errtrack

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/project-init/devex/internal/sre/errtrack/report"
)

const (
	fixturePath = "testdata/issue.json"
	goldenPath  = "testdata/report.json"
)

var update = flag.Bool("update", false, "rewrite "+goldenPath+" from the fixture")

// loadFixture decodes testdata/issue.json, a sanitised copy of a real response.
func loadFixture(t *testing.T) issueResponse {
	t.Helper()

	raw, err := os.ReadFile(fixturePath)
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

// TestToReportGolden maps a real response and compares the whole document to a
// committed expectation.
//
// One assertion over the entire output, rather than one per field: it covers
// the schema, the summary, the timestamps, the links and every null, and it
// fails on anything that changes shape. A field-by-field version of this missed
// a wrong issue URL in the documented example for days, because no test ever
// compared what the mapper produces against what we say it produces.
//
// Regenerate with: go test ./internal/sre/errtrack/ -run TestToReportGolden -update
// The golden file is in .prettierignore, since -update writes Go's indentation
// and prettier would reformat it straight back.
func TestToReportGolden(t *testing.T) {
	got := toReport(loadFixture(t), mapInput{Site: "datadoghq.com", FetchedAt: testNow})

	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded = append(encoded, '\n')

	if *update {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", goldenPath)

		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	// Compared as documents rather than bytes: prettier formats JSON in this
	// repository and keeps short arrays on one line, where MarshalIndent
	// expands them. Whitespace is the formatter's business, not this test's.
	var wantDoc, gotDoc any
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("golden is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(encoded, &gotDoc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(wantDoc, gotDoc) {
		t.Errorf("report does not match %s; rerun with -update if the change is intended\n--- want ---\n%s\n--- got ---\n%s",
			goldenPath, want, encoded)
	}
}

// TestToReportWithSourceIntegration covers what the golden fixture cannot: the
// case where Source Code Integration is configured, so samples carry git
// metadata and a parsed stack. There is no real response to capture for this
// yet, so the input is built by hand.
func TestToReportWithSourceIntegration(t *testing.T) {
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

	resp := issueResponse{
		Data: issueData{
			ID: "aae10c1c-dacd-11f1-a39f-da7ad0900002",
			Attributes: issueAttributes{
				ErrorType:    "*errors.errorString",
				ErrorMessage: "boom",
				Service:      "login-ui-app",
				FilePath:     "/app/pkg/kratos/admin.go",
			},
		},
		Included: []included{{
			Type: sampleType,
			Attributes: sampleAttributes{
				Biases:           []string{gitMetadataBias},
				GitRepositoryURL: serviceRepo,
				GitCommitSHA:     serviceSHA,
				ErrorStack: errorStack{Frames: []stackFrame{
					top,
					serviceFrame("admin.go:142", "pkg/kratos/admin.go", 142),
					{Text: "vendored", GitFileLocation: &gitFileLocation{
						FilePath: "mux.go", FirstLine: ptr(90),
						RepoID: "github.com/go-chi/chi", Ref: vendorSHA,
					}},
					serviceFrame("dropped by the cap", "internal/server/router.go", 214),
				}},
			},
		}},
	}

	got := toReport(resp, mapInput{Site: "datadoghq.com", FetchedAt: testNow})

	if len(got.Code.Frames) != report.MaxTicketFrames || !got.Code.FramesTruncated {
		t.Fatalf("frames = %d, truncated = %v; want %d and true",
			len(got.Code.Frames), got.Code.FramesTruncated, report.MaxTicketFrames)
	}

	// The top frame is preferred over the issue's /app-prefixed file_path, and
	// it brings a line number the issue attributes do not have.
	if got.Code.Location.File != "pkg/kratos/admin.go" {
		t.Errorf("Location.File = %q, want the frame's repository path", got.Code.Location.File)
	}
	if got.Code.Location.Line == nil || *got.Code.Location.Line != 118 {
		t.Errorf("Location.Line = %v, want 118", got.Code.Location.Line)
	}

	want := "https://github.com/dailypay/login-ui/blob/" + serviceSHA + "/pkg/kratos/admin.go#L118"
	if got.Code.Frames[0].SourceURL != want {
		t.Errorf("SourceURL = %q, want %q", got.Code.Frames[0].SourceURL, want)
	}

	// The reason Frame carries its own repository and commit: a frame inside a
	// dependency has to link into that dependency, not into this service.
	vendored := got.Code.Frames[2]
	wantVendored := "https://github.com/go-chi/chi/blob/" + vendorSHA + "/mux.go#L90"
	if vendored.SourceURL != wantVendored {
		t.Errorf("vendored SourceURL = %q, want %q", vendored.SourceURL, wantVendored)
	}
	if got.Code.Frames[1].RepositoryURL != serviceRepo {
		t.Errorf("frame 1 repository = %q, want the service's", got.Code.Frames[1].RepositoryURL)
	}
}

// TestToReportEnvironment covers the only field with two possible sources.
func TestToReportEnvironment(t *testing.T) {
	tests := map[string]struct {
		override string
		eventEnv string
		want     *string
	}{
		"from the event":            {eventEnv: "production", want: ptr("production")},
		"the flag wins":             {override: "staging", eventEnv: "production", want: ptr("staging")},
		"null when neither has one": {},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp := loadFixture(t)
			resp.Included[0].Attributes.Event.Env = tt.eventEnv

			got := toReport(resp, mapInput{EnvOverride: tt.override, FetchedAt: testNow}).Error.Environment

			switch {
			case tt.want == nil && got != nil:
				t.Errorf("Environment = %q, want null", *got)
			case tt.want != nil && got == nil:
				t.Errorf("Environment = null, want %q", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("Environment = %q, want %q", *got, *tt.want)
			}
		})
	}
}

// TestToReportNoSample covers a response with no sample, which is what arrives
// if the bias filter matches nothing. The issue attributes alone are still
// worth reporting, so this must not panic or lose them.
func TestToReportNoSample(t *testing.T) {
	resp := loadFixture(t)
	resp.Included = nil

	got := toReport(resp, mapInput{Site: "datadoghq.com", FetchedAt: testNow})

	if got.Error.Type == "" || got.Links.Issue == "" {
		t.Errorf("lost the issue attributes along with the sample: %+v", got.Error)
	}
	if got.Error.Environment != nil {
		t.Error("Environment should be null with no sample to read it from")
	}
}
