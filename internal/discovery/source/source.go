// Package source resolves where a bundle's files live once they reach the default branch, so
// published issues can link to the discovery document rather than name a path the reader cannot
// open.
package source

import (
	"net/url"
	"os/exec"
	"strings"
)

// DocumentURL builds a GitHub blob URL for file inside directory. The URL points at the default
// branch, so it resolves once the branch holding the bundle merges. Every step is best-effort and
// offline: a bundle outside a Git checkout, a checkout without a GitHub origin, or a repository
// whose default branch cannot be determined locally all yield an empty string, and callers omit
// the link rather than publish a broken one.
func DocumentURL(directory string, file string) string {
	// url.JoinPath resolves "..", so a file climbing out of the bundle would address an
	// unrelated repository. Bundle loading rejects such a path already; refusing it here keeps
	// that true for any other caller.
	if file == "" || strings.Contains(file, "..") {
		return ""
	}
	owner, repository := parseGitHubRemote(gitOutput(directory, "remote", "get-url", "origin"))
	if owner == "" || repository == "" {
		return ""
	}
	branch := defaultBranch(directory)
	if branch == "" {
		return ""
	}
	// Git reports the path relative to the repository root, already slash-separated and with
	// symlinks resolved, so no local path arithmetic is needed to place the file.
	prefix := gitOutput(directory, "rev-parse", "--show-prefix")
	documentURL, err := url.JoinPath("https://github.com", owner, repository, "blob", branch, prefix+file)
	if err != nil {
		return ""
	}

	return documentURL
}

// defaultBranch reads the branch origin points at, falling back to the conventional names because
// a clone only records origin/HEAD when it was cloned rather than added by hand.
func defaultBranch(directory string) string {
	if head := gitOutput(directory, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); head != "" {
		return strings.TrimPrefix(head, "origin/")
	}
	for _, candidate := range []string{"main", "master"} {
		if gitOutput(directory, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+candidate) != "" {
			return candidate
		}
	}

	return ""
}

// parseGitHubRemote accepts the SCP-like, SSH, and HTTPS forms Git writes for a GitHub remote and
// reports the owner and repository. Any other host yields empty strings.
func parseGitHubRemote(remote string) (string, string) {
	if remote == "" {
		return "", ""
	}
	trimmed := strings.TrimSuffix(remote, ".git")
	switch {
	case strings.HasPrefix(trimmed, "git@github.com:"):
		trimmed = strings.TrimPrefix(trimmed, "git@github.com:")
	case strings.HasPrefix(trimmed, "ssh://git@github.com/"):
		trimmed = strings.TrimPrefix(trimmed, "ssh://git@github.com/")
	case strings.HasPrefix(trimmed, "https://github.com/"):
		trimmed = strings.TrimPrefix(trimmed, "https://github.com/")
	default:
		return "", ""
	}
	owner, repository, found := strings.Cut(strings.Trim(trimmed, "/"), "/")
	if !found || owner == "" || repository == "" || strings.Contains(repository, "/") {
		return "", ""
	}

	return owner, repository
}

func gitOutput(directory string, args ...string) string {
	output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}
