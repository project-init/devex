package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/project-init/devex/internal/components"
	"github.com/project-init/devex/internal/components/db"
)

func main() {
	if len(os.Args) != 2 {
		usage()
	}
	cfg, err := components.LoadConfig(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Using Configuration - %+v", cfg)

	err = outputDBFiles(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Println("Usage: components <configuration_file_path>")
}

func outputDBFiles(cfg *components.Config) error {
	if cfg.DB.SchemaName == "" {
		return nil
	}

	return db.OutputFiles(cfg.DB, filepath.Join(cfg.OutputDirectory, "db"), "")
}
