package main

import (
	"os"

	"github.com/project-init/devex/internal/sre"
)

func main() {
	if err := sre.Execute(); err != nil {
		os.Exit(1)
	}
}
