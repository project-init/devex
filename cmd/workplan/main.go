package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"

	"github.com/project-init/devex/internal/workplan"
	"github.com/project-init/devex/internal/workplan/jira"
	"github.com/project-init/devex/internal/workplan/problem"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		usage()
	}

	switch os.Args[1] {
	case "generate":
		generateFiles()
	case "publish":
		planWorkInJira()
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

func generateFiles() {
	workplanPath := os.Args[2]
	problemOutputPath := path.Join(path.Dir(workplanPath), "problem.md")
	err := problem.GenerateProblemTemplate(problemOutputPath)
	if err != nil {
		log.Fatal(err)
	}

	err = workplan.GenerateWorkplanTemplate(workplanPath)
	if err != nil {
		log.Fatal(err)
	}
}

func planWorkInJira() {
	fmt.Println("Let's plan some work...")

	workplanPath := os.Args[1]
	wp, err := workplan.LoadWorkplan(workplanPath)
	if err != nil {
		log.Fatal(err)
	}
	jiraClient, err := jira.New()
	if err != nil {
		log.Fatal(err)
	}

	if err = jiraClient.Create(context.Background(), wp); err != nil {
		log.Fatal(err)
	}
}
