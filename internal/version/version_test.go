package version

import (
	"runtime/debug"
	"testing"
)

func TestFormat(t *testing.T) {
	buildInfo := &debug.BuildInfo{
		Main:     debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}},
	}
	tests := []struct {
		name    string
		version string
		commit  string
		info    *debug.BuildInfo
		want    string
	}{
		{name: "stamped release", version: "v2.0.0", commit: "fedcba9", info: buildInfo, want: "v2.0.0 (fedcba9)"},
		{name: "build info fallback", info: buildInfo, want: "v1.2.3 (0123456)"},
		{name: "short revision", info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}}, want: "(devel) (abc)"},
		{name: "no information", want: "(devel)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := format(test.version, test.commit, test.info); got != test.want {
				t.Errorf("format() = %q, want %q", got, test.want)
			}
		})
	}
}
