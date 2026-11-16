// Package version reports which devex build is running.
package version

import "runtime/debug"

// goreleaser sets these with -ldflags "-X"; see .goreleaser.yaml.
var (
	version string
	commit  string
)

const shortCommitLen = 7

// String returns the build's version and commit, such as "v1.4.0 (abc1234)". Release builds
// carry the tag goreleaser stamps. Other builds fall back to the module version and VCS
// revision Go records, and report "(devel)" when neither exists.
func String() string {
	info, _ := debug.ReadBuildInfo()
	return format(version, commit, info)
}

func format(version, commit string, info *debug.BuildInfo) string {
	if info != nil {
		if version == "" {
			version = info.Main.Version
		}
		if commit == "" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					commit = setting.Value[:min(shortCommitLen, len(setting.Value))]
				}
			}
		}
	}
	if version == "" {
		version = "(devel)"
	}
	if commit == "" {
		return version
	}
	return version + " (" + commit + ")"
}
