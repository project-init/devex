package templates

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
)

//go:embed discovery.md.tmpl work-breakdown.yaml.tmpl
var files embed.FS

type templateData struct {
	ID                string
	Title             string
	DefaultLabelsYAML string
}

type GenerateOptions struct {
	DefaultLabels []string
	// Date names the bundle's iteration folder and ends its discovery ID. The zero value means
	// today.
	Date time.Time
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
	date := options.Date
	if date.IsZero() {
		date = time.Now()
	}
	// Each iteration gets its own folder and discovery ID, so a later iteration publishes fresh
	// issues and leaves the work an earlier one published, and the document it links, intact.
	iteration := date.Format(time.DateOnly)
	directory := filepath.Join(baseDirectory, slug, iteration)
	if err := os.MkdirAll(filepath.Dir(directory), 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(directory, 0o755); errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("discovery bundle %s already exists; edit it instead of starting another iteration today", directory)
	} else if err != nil {
		return "", err
	}
	data := templateData{
		ID:                slug + "-" + iteration,
		Title:             titleFromName(name),
		DefaultLabelsYAML: formatLabelsYAML(options.DefaultLabels),
	}
	if err := renderBundle(directory, data); err != nil {
		// The folder is this run's alone, so removing it frees the day's iteration for a retry.
		_ = os.RemoveAll(directory)
		return "", err
	}
	return filepath.Abs(directory)
}

func renderBundle(directory string, data templateData) error {
	if err := renderNew(filepath.Join(directory, "discovery.md"), "discovery.md.tmpl", data); err != nil {
		return err
	}
	if err := renderNew(filepath.Join(directory, "work-breakdown.yaml"), "work-breakdown.yaml.tmpl", data); err != nil {
		return err
	}
	return writeNew(filepath.Join(directory, ".gitignore"), []byte(".publish/\n"))
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
