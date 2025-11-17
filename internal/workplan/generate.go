package workplan

import (
	"embed"
	"log"
	"os"
	"path"
	"text/template"

	"github.com/project-init/devex/internal/workplan/problem"
)

//go:embed workplan.yaml.tmpl
var workplanFs embed.FS

func GenerateFiles(workplanPath string) error {
	problemOutputPath := path.Join(path.Dir(workplanPath), "problem.md")
	err := problem.GenerateProblemTemplate(problemOutputPath)
	if err != nil {
		return err
	}

	err = generateWorkplanTemplate(workplanPath)
	if err != nil {
		return err
	}

	return nil
}

func generateWorkplanTemplate(outputPath string) error {
	templ, err := template.ParseFS(workplanFs, "workplan.yaml.tmpl")
	if err != nil {
		return err
	}

	err = os.MkdirAll(path.Dir(outputPath), os.ModePerm)
	if err != nil {
		return err
	}

	templatizedFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	defer func(f *os.File) {
		err = f.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(templatizedFile)

	return templ.Execute(templatizedFile, nil)
}
