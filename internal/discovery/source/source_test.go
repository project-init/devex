package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDocumentURL(t *testing.T) {
	repository := newRepository(t, "git@github.com:project-init/business-platform.git")
	bundle := filepath.Join(repository, "docs", "investigations", "audit logs")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}

	got := DocumentURL(bundle, "discovery.md")
	// A space in the path must survive as an escape rather than break the link.
	want := "https://github.com/project-init/business-platform/blob/main/docs/investigations/audit%20logs/discovery.md"
	if got != want {
		t.Fatalf("DocumentURL() = %q, want %q", got, want)
	}
}

// A cloned checkout records origin/HEAD, which names the default branch outright.
func TestDocumentURLFollowsOriginHead(t *testing.T) {
	repository := newRepository(t, "git@github.com:project-init/devex.git")
	runGit(t, repository, "update-ref", "refs/remotes/origin/trunk",
		gitOutput(repository, "rev-parse", "refs/remotes/origin/main"))
	runGit(t, repository, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	got := DocumentURL(repository, "discovery.md")
	want := "https://github.com/project-init/devex/blob/trunk/discovery.md"
	if got != want {
		t.Fatalf("DocumentURL() = %q, want %q", got, want)
	}
}

// A bundle whose repository publishes nowhere gets no footer, rather than a link that 404s.
func TestDocumentURLWithoutGitHubRemote(t *testing.T) {
	repository := newRepository(t, "git@gitlab.test:project-init/business-platform.git")

	if got := DocumentURL(repository, "discovery.md"); got != "" {
		t.Fatalf("DocumentURL() = %q, want empty", got)
	}
}

func TestDocumentURLOutsideARepository(t *testing.T) {
	if got := DocumentURL(t.TempDir(), "discovery.md"); got != "" {
		t.Fatalf("DocumentURL() = %q, want empty", got)
	}
}

func TestParseGitHubRemote(t *testing.T) {
	tests := []struct {
		name           string
		remote         string
		wantOwner      string
		wantRepository string
	}{
		{
			name:           "scp form",
			remote:         "git@github.com:project-init/devex.git",
			wantOwner:      "project-init",
			wantRepository: "devex",
		},
		{
			name:           "ssh form",
			remote:         "ssh://git@github.com/project-init/devex.git",
			wantOwner:      "project-init",
			wantRepository: "devex",
		},
		{
			name:           "https without suffix",
			remote:         "https://github.com/project-init/devex",
			wantOwner:      "project-init",
			wantRepository: "devex",
		},
		{name: "other host", remote: "https://gitlab.test/project-init/devex.git"},
		{name: "no repository", remote: "https://github.com/project-init"},
		{name: "absent"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner, repository := parseGitHubRemote(test.remote)
			if owner != test.wantOwner || repository != test.wantRepository {
				t.Fatalf("parseGitHubRemote(%q) = %q, %q, want %q, %q",
					test.remote, owner, repository, test.wantOwner, test.wantRepository)
			}
		})
	}
}

// newRepository builds a checkout carrying the origin/main reference that DocumentURL reads. A
// hand-added remote records no origin/HEAD, so the branch resolves through the fallback.
func newRepository(t *testing.T, remote string) string {
	t.Helper()
	directory := t.TempDir()
	runGit(t, directory, "init", "--quiet")
	runGit(t, directory, "remote", "add", "origin", remote)
	runGit(t, directory, "update-ref", "refs/remotes/origin/main",
		gitOutput(directory, "hash-object", "-t", "tree", "-w", "--stdin"))

	return directory
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

// url.JoinPath resolves "..", so a document escaping the bundle would address another repository.
func TestDocumentURLRejectsAnEscapingPath(t *testing.T) {
	repository := newRepository(t, "git@github.com:project-init/devex.git")

	if got := DocumentURL(repository, "../../evil/repo/blob/main/x.md"); got != "" {
		t.Fatalf("DocumentURL() = %q, want empty", got)
	}
}
