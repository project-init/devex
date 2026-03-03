package main

import (
	"fmt"
	"log"
	"os"

	"github.com/manifoldco/promptui"
	"github.com/project-init/devex/internal/release"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help":
			usage()
		default:
			usage()
		}
	}

	current, err := release.FetchLatestTag()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Current version: %s\n", current)

	bumpType, err := selectBumpType()
	if err != nil {
		log.Fatal(err)
	}

	next := release.Bump(current, bumpType)

	if bumpType == release.BumpMajor {
		fmt.Printf("\n⚠️  WARNING: Major version bump indicates BREAKING CHANGES!\n")
	}

	fmt.Printf("\nNew version will be: %s\n", next)

	if err = confirmRelease(next); err != nil {
		fmt.Println("Tag creation cancelled.")
		os.Exit(1)
	}

	fmt.Printf("Creating and pushing tag %s...\n", next)
	if err = release.CreateAndPushTag(next.String()); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Tag %s created and pushed successfully!\n", next)
}

func selectBumpType() (release.BumpType, error) {
	prompt := promptui.Select{
		Label: "Select version bump type",
		Items: []string{
			"patch  (bug fixes)",
			"minor  (new features, backward compatible)",
			"major  (breaking changes)",
		},
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return 0, err
	}

	switch idx {
	case 0:
		return release.BumpPatch, nil
	case 1:
		return release.BumpMinor, nil
	case 2:
		return release.BumpMajor, nil
	default:
		return 0, fmt.Errorf("invalid selection")
	}
}

func confirmRelease(v release.Version) error {
	prompt := promptui.Prompt{
		Label:     fmt.Sprintf("Create and push tag %s", v),
		IsConfirm: true,
	}

	_, err := prompt.Run()
	return err
}

func usage() {
	usageOutput := `
Usage:
	release

	Fetches the latest git tag, prompts for a version bump type
	(major/minor/patch), and creates + pushes the new tag.

Commands:
	help - Print this help message.
`
	log.Fatal(usageOutput)
}
