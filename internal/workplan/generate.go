package workplan

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path"
	"text/template"
	"time"

	"github.com/project-init/devex/internal/workplan/problem"
)

//go:embed workplan.yaml.tmpl
var workplanFs embed.FS

func GenerateFiles(workplanDirectory string, workplanName string) error {
	problemOutputPath := path.Join(workplanDirectory, datedWorkplanName(workplanName), "problem.md")
	err := problem.GenerateProblemTemplate(problemOutputPath)
	if err != nil {
		return err
	}

	workplanPath := path.Join(workplanDirectory, datedWorkplanName(workplanName), "workplan.yaml")
	err = generateWorkplanTemplate(workplanPath)
	if err != nil {
		return err
	}

	return nil
}

func datedWorkplanName(name string) string {
	year, month, day := time.Now().Date()
	return fmt.Sprintf("%4d_%2d_%2d_%s", year, month, day, name)
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
