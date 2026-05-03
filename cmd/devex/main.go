package main

import (
	"os"

	"github.com/project-init/devex/internal/root"
)

func main() {
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
