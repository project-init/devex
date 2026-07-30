package release

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/manifoldco/promptui"
)

func createAndPushTag(tag string) error {
	if err := runGit("tag", "-a", tag, "-m", "Release "+tag); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if err := runGit("push", "origin", tag); err != nil {
		return fmt.Errorf("failed to push tag: %w", err)
	}

	return nil
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func confirmRelease(v version) error {
	prompt := promptui.Prompt{
		Label:     fmt.Sprintf("Create and push tag %s", v),
		IsConfirm: true,
	}

	_, err := prompt.Run()
	return err
}

func getActionsURL() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	remote := strings.TrimSpace(string(out))
	remote = strings.TrimSuffix(remote, ".git")

	// SSH format: git@github.com:org/repo
	if path, ok := strings.CutPrefix(remote, "git@github.com:"); ok {
		return fmt.Sprintf("https://github.com/%s/actions", path), nil
	}

	// HTTPS format: https://github.com/org/repo
	if _, ok := strings.CutPrefix(remote, "https://github.com/"); ok {
		return remote + "/actions", nil
	}

	return "", fmt.Errorf("unrecognized remote format: %s", remote)
}
