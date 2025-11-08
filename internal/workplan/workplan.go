package workplan

import (
	"embed"
	"os"
	"path"
	"text/template"

	"github.com/project-init/devex/internal/workplan/models"
	"gopkg.in/yaml.v3"
)

//go:embed workplan.yaml.tmpl
var problem embed.FS

func LoadWorkplan(workplanPath string) (*models.Workplan, error) {
	bytes, err := os.ReadFile(workplanPath)
	if err != nil {
		return nil, err
	}

	var workplan models.Workplan
	if err = yaml.Unmarshal(bytes, &workplan); err != nil {
		return nil, err
	}
	return &workplan, nil
}

func GenerateWorkplanTemplate(outputPath string) error {
	templ, err := template.ParseFS(problem, "workplan.yaml.tmpl")
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
