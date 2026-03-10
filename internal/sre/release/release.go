package release

import (
	"fmt"
	"os/exec"

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
