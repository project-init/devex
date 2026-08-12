package html

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	baseLocale                 = "en-US"
	messagesCatalogName        = "messages.gotext.json"
	nonStringTranslationMarker = "[plural/select]"
)

//go:embed report.html.tmpl
var reportFs embed.FS

type catalog struct {
	Language string    `json:"language"`
	Messages []message `json:"messages"`
}

// gotextID handles the gotext format where "id" may be a plain string or a
// [key, fallbackText] array; we always use the first element as the ID.
type gotextID string

func (g *gotextID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*g = gotextID(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) == 0 {
		return fmt.Errorf("gotext id array is empty")
	}
	*g = gotextID(arr[0])
	return nil
}

type message struct {
	ID          gotextID `json:"id"`
	Message     string   `json:"message"`
	Translation any      `json:"translation"`
}

type reportRow struct {
	English      string
	Translations []string
}

type reportData struct {
	Locales []string
	Rows    []reportRow
}

// Generate reads each locale's messages.gotext.json catalog under localesDir and writes a
// single combined HTML report to outputPath, with one column per locale and one row per
// message id, showing the English source text alongside every locale's translation.
func Generate(localesDir string, outputPath string, stdout io.Writer, stderr io.Writer) error {
	if err := validateConfig(localesDir, outputPath); err != nil {
		return err
	}

	directories, err := filepath.Glob(filepath.Join(localesDir, "*/"))
	if err != nil {
		return err
	}
	if len(directories) == 0 {
		return fmt.Errorf("no locale directories found in %s", localesDir)
	}

	// locale -> id -> translation
	translations := make(map[string]map[string]string)
	// id -> English message text (from en-US "message" field)
	englishText := make(map[string]string)
	// all known ids across every locale
	ids := make(map[string]struct{})

	for _, directory := range directories {
		locale := filepath.Base(filepath.Clean(directory))
		catalogPath := filepath.Join(directory, messagesCatalogName)
		cat, err := loadCatalog(catalogPath)
		if err != nil {
			return err
		}

		if cat.Language != locale {
			_, _ = fmt.Fprintf(
				stderr,
				"warning: %s declares language %q but was loaded from locale directory %q\n",
				catalogPath,
				cat.Language,
				locale,
			)
		}

		for _, msg := range cat.Messages {
			ids[string(msg.ID)] = struct{}{}
		}

		if locale == baseLocale {
			for _, msg := range cat.Messages {
				englishText[string(msg.ID)] = msg.Message
			}
			continue
		}

		byID := make(map[string]string, len(cat.Messages))
		for _, msg := range cat.Messages {
			translation, ok := msg.Translation.(string)
			if !ok {
				_, _ = fmt.Fprintf(
					stderr,
					"warning: %s has a non-string translation for %q; rendering as %s\n",
					catalogPath,
					msg.ID,
					nonStringTranslationMarker,
				)
				translation = nonStringTranslationMarker
			}
			byID[string(msg.ID)] = translation
		}
		translations[locale] = byID
	}

	if len(translations) == 0 {
		return fmt.Errorf("no non-%s locale catalogs found in %s", baseLocale, localesDir)
	}

	locales := make([]string, 0, len(translations))
	for locale := range translations {
		locales = append(locales, locale)
	}
	sort.Strings(locales)

	sortedIDs := make([]string, 0, len(ids))
	for id := range ids {
		sortedIDs = append(sortedIDs, id)
	}
	sort.Strings(sortedIDs)

	rows := make([]reportRow, 0, len(sortedIDs))
	for _, id := range sortedIDs {
		english := englishText[id]
		if english == "" {
			english = id
		}
		row := reportRow{English: english, Translations: make([]string, len(locales))}
		allMatch := true
		for i, locale := range locales {
			t := translations[locale][id]
			row.Translations[i] = t
			if t != english {
				allMatch = false
			}
		}
		if allMatch {
			continue
		}
		rows = append(rows, row)
	}

	if err := writeReport(outputPath, reportData{Locales: locales, Rows: rows}); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "wrote %s (%d locales, %d strings)\n", outputPath, len(locales), len(rows))
	return nil
}

func validateConfig(localesDir string, outputPath string) error {
	missing := make([]string, 0, 2)
	if localesDir == "" {
		missing = append(missing, "localize.localesDir")
	}
	if outputPath == "" {
		missing = append(missing, "localize.html.outputPath")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func loadCatalog(path string) (*catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cat catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cat, nil
}

func writeReport(outputPath string, data reportData) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}

	templ, err := template.ParseFS(reportFs, "report.html.tmpl")
	if err != nil {
		return err
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	return templ.Execute(file, data)
}
