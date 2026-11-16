package problem

import (
	"embed"
	"errors"
	"os"
	"path"
	"text/template"
)

//go:embed problem.md.tmpl
var problem embed.FS

func GenerateProblemTemplate(outputPath string) error {
	templ, err := template.ParseFS(problem, "problem.md.tmpl")
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
	return errors.Join(templ.Execute(templatizedFile, nil), templatizedFile.Close())
}
