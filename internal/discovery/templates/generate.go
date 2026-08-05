package templates

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

//go:embed discovery.md.tmpl work-breakdown.yaml.tmpl
var files embed.FS

type templateData struct {
	Slug              string
	Title             string
	DefaultLabelsYAML string
}

type GenerateOptions struct {
	DefaultLabels []string
}

var invalidSlug = regexp.MustCompile(`[^a-z0-9]+`)

func Generate(baseDirectory string, name string) (string, error) {
	return GenerateWithOptions(baseDirectory, name, GenerateOptions{})
}

func GenerateWithOptions(baseDirectory string, name string, options GenerateOptions) (string, error) {
	slug := strings.Trim(invalidSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		return "", fmt.Errorf("discovery name must contain letters or digits")
	}
	title := titleFromName(name)
	directory := filepath.Join(baseDirectory, slug)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	data := templateData{
		Slug:              slug,
		Title:             title,
		DefaultLabelsYAML: formatLabelsYAML(options.DefaultLabels),
	}
	if err := renderNew(filepath.Join(directory, "discovery.md"), "discovery.md.tmpl", data); err != nil {
		return "", err
	}
	if err := renderNew(filepath.Join(directory, "work-breakdown.yaml"), "work-breakdown.yaml.tmpl", data); err != nil {
		return "", err
	}
	if err := writeNew(filepath.Join(directory, ".gitignore"), []byte(".publish/\n")); err != nil {
		return "", err
	}
	return filepath.Abs(directory)
}

func formatLabelsYAML(labels []string) string {
	if len(labels) == 0 {
		return " []"
	}
	var output strings.Builder
	for _, label := range labels {
		_, _ = fmt.Fprintf(&output, "\n      - %q", label)
	}
	return output.String()
}

func renderNew(outputPath string, templateName string, data templateData) error {
	templ, err := template.ParseFS(files, templateName)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := templ.Execute(&output, data); err != nil {
		return err
	}
	return writeNew(outputPath, output.Bytes())
}

func writeNew(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func titleFromName(name string) string {
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(name))
	for index, word := range words {
		if len(word) > 0 {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
