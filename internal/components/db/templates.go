package db

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

var (
	nonOverwriteFiles = []string{
		"init/100-bootstrap.sql",
		"init/200-schema.sql",
		"init/300-users.sql",
		"seeddata/seed.sql",
		"seeddata/users.csv",
		"sqlc/users.sql",
		"sqlc.yaml",
		"structure.sql",
	}
)

func OutputFiles(config Config, outputDirectory string, entryPrefix string) error {
	err := os.MkdirAll(filepath.Join(outputDirectory, entryPrefix), 0755)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(templates, filepath.Join("templates", entryPrefix))
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			err = OutputFiles(config, outputDirectory, filepath.Join(entryPrefix, entry.Name()))
			if err != nil {
				return err
			}
			continue
		}

		outputPath := strings.ReplaceAll(filepath.Join(outputDirectory, entryPrefix, entry.Name()), ".tmpl", "")
		if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
			if slices.Contains(nonOverwriteFiles, filepath.Join(entryPrefix, strings.ReplaceAll(entry.Name(), ".tmpl", ""))) {
				fmt.Printf("Skipping Overwrite of %s\n", outputPath)
				continue
			}
		}

		if strings.HasSuffix(entry.Name(), ".tmpl") {
			templ, err := template.ParseFS(templates, filepath.Join("templates", entryPrefix, entry.Name()))
			if err != nil {
				return err
			}

			templatizedFile, err := os.Create(outputPath)
			if err != nil {
				return err
			}

			err = templ.Execute(templatizedFile, config)
			if err != nil {
				err = templatizedFile.Close()
				if err != nil {
					return err
				}
				return err
			}

			err = templatizedFile.Close()
			if err != nil {
				return err
			}
		} else {
			data, err := templates.ReadFile(fmt.Sprintf("templates/%s", filepath.Join(entryPrefix, entry.Name())))
			if err != nil {
				return err
			}
			err = os.WriteFile(outputPath, data, 0644)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
