package migrate

import (
	"strings"
	"testing"

	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestResolveLaunchType(t *testing.T) {
	cases := []struct {
		name  string
		flag  string
		valid bool
		want  ecstypes.LaunchType
	}{
		{name: "fargate", flag: "FARGATE", valid: true, want: ecstypes.LaunchTypeFargate},
		{name: "ec2", flag: "EC2", valid: true, want: ecstypes.LaunchTypeEc2},
		{name: "lowercase is normalized", flag: "ec2", valid: true, want: ecstypes.LaunchTypeEc2},
		{name: "unknown value", flag: "SERVERLESS"},
		{name: "empty value", flag: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &options{launchType: tc.flag}
			got, err := opts.resolveLaunchType()

			if !tc.valid {
				if err == nil {
					t.Fatalf("resolveLaunchType(%q) = %q, want an error", tc.flag, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveLaunchType(%q): %v", tc.flag, err)
			}
			if got != tc.want {
				t.Fatalf("resolveLaunchType(%q) = %q, want %q", tc.flag, got, tc.want)
			}
		})
	}
}

func TestCommandDefaultsToFargate(t *testing.T) {
	t.Setenv("LAUNCH_TYPE", "")

	cmd := Command()
	flag := cmd.Flags().Lookup("launch-type")
	if flag == nil {
		t.Fatal("--launch-type flag is not registered")
	}
	if flag.DefValue != string(ecstypes.LaunchTypeFargate) {
		t.Fatalf("--launch-type default = %q, want %q", flag.DefValue, ecstypes.LaunchTypeFargate)
	}
}

func TestCommandReadsLaunchTypeFromEnvironment(t *testing.T) {
	t.Setenv("LAUNCH_TYPE", "EC2")

	cmd := Command()
	flag := cmd.Flags().Lookup("launch-type")
	if flag == nil {
		t.Fatal("--launch-type flag is not registered")
	}
	if flag.DefValue != string(ecstypes.LaunchTypeEc2) {
		t.Fatalf("--launch-type default = %q, want %q", flag.DefValue, ecstypes.LaunchTypeEc2)
	}
}

func TestResolveLaunchTypeErrorListsValidValues(t *testing.T) {
	opts := &options{launchType: "SERVERLESS"}

	_, err := opts.resolveLaunchType()
	if err == nil {
		t.Fatal("resolveLaunchType() accepted an invalid launch type")
	}
	if !strings.Contains(err.Error(), string(ecstypes.LaunchTypeEc2)) {
		t.Fatalf("error does not name the valid values: %v", err)
	}
}
