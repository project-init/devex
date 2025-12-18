package db

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

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

		outputPath := filepath.Join(outputDirectory, entryPrefix, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmpl") {
			templ, err := template.ParseFS(templates, filepath.Join("templates", entryPrefix, entry.Name()))
			if err != nil {
				return err
			}

			templatizedFile, err := os.Create(strings.ReplaceAll(outputPath, ".tmpl", ""))
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
