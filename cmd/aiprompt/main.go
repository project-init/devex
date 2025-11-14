package main

import (
	"log"
	"os"

	"github.com/project-init/devex/internal/aiprompt"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage:\n\naipprompt <prompt_directory>")
	}

	prompts, err := aiprompt.LoadPrompts(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	if len(prompts) == 0 {
		log.Fatal("No prompts found")
	}

	err = aiprompt.RunGemini(prompts)
	if err != nil {
		log.Fatal(err)
	}
}
