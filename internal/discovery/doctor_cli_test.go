package discovery

import (
	"bytes"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/doctor"
)

func TestPrintDoctorReportGroupsFailuresLast(t *testing.T) {
	report := doctor.Report{Checks: []doctor.Check{
		{Severity: doctor.SeverityFail, Name: "configuration", Detail: "invalid\nsecond line", Remedy: "fix config"},
		{Severity: doctor.SeverityFlag, Name: "skill:cursor", Detail: "optional harness missing"},
		{Severity: doctor.SeverityPass, Name: "cli", Detail: "available"},
		{Severity: doctor.SeverityWarn, Name: "credentials:jira", Detail: "not set"},
	}}
	var output bytes.Buffer
	err := printDoctorReport(&output, report)
	if err == nil {
		t.Fatal("printDoctorReport() returned nil with a failure")
	}
	text := output.String()
	passIndex := strings.Index(text, "Passed")
	flagIndex := strings.Index(text, "Flagged")
	failureIndex := strings.Index(text, "Failures")
	if passIndex < 0 || flagIndex <= passIndex || failureIndex <= flagIndex {
		t.Fatalf("doctor groups are not ordered:\n%s", text)
	}
	if !strings.Contains(text, "[FAIL] configuration: invalid\n         second line") {
		t.Fatalf("failure is not formatted as an indented error:\n%s", text)
	}
}

func TestPrintDoctorReportAllowsAdditionalHarnessFlags(t *testing.T) {
	report := doctor.Report{Checks: []doctor.Check{
		{Severity: doctor.SeverityPass, Name: "skill:codex", Detail: "installed"},
		{Severity: doctor.SeverityFlag, Name: "skill:claude", Detail: "not installed"},
	}}
	var output bytes.Buffer
	if err := printDoctorReport(&output, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Discovery is ready (1 flag(s), 0 warning(s)).") {
		t.Fatalf("output = %q", output.String())
	}
}
