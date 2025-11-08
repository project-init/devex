package main

import (
	"context"
	"log"
	"os"

	"github.com/project-init/devex/internal/workplan"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		usage()
	}

	switch os.Args[1] {
	case "generate":
		err := workplan.GenerateFiles(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
	case "publish":
		err := workplan.PublishWorkPlanToJira(context.Background(), os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
	case "help":
		usage()
	default:
		usage()
	}
}

func usage() {
	usageOutput := `
Usage:
	workplan [command] [arguments]

Commands:
	generate <workplan_path> - Generate a workplan and problem markdown based on the current template(s).
	publish <workplan_path> - Publish a workplan to JIRA.
	help - Print this help message.
`
	log.Fatal(usageOutput)
}
