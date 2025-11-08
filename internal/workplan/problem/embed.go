package problem

import (
	"embed"
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
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			panic(err)
		}
	}(templatizedFile)

	return templ.Execute(templatizedFile, nil)
}
