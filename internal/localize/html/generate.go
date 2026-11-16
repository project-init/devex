package html

import (
	"embed"
	"fmt"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/localize/catalog"
	"github.com/project-init/devex/internal/ui"
)

const (
	nonStringTranslationMarker = "[plural/select]"
)

//go:embed report.html.tmpl
var reportFs embed.FS

type localeCoverage struct {
	Name       string
	Translated int
	Total      int
}

// Percent is the translated share, rounded down so a locale shows 100 only when complete.
func (c localeCoverage) Percent() int {
	if c.Total == 0 {
		return 100
	}
	return c.Translated * 100 / c.Total
}

func (c localeCoverage) Level() string {
	switch percent := c.Percent(); {
	case percent == 100:
		return "complete"
	case percent >= 80:
		return "partial"
	default:
		return "low"
	}
}

type reportCell struct {
	Text    string
	Missing bool
}

type reportRow struct {
	English string
	Cells   []reportCell
	Missing bool
}

type reportData struct {
	Locales     []localeCoverage
	Rows        []reportRow
	Total       int
	MissingRows int
}

// Generate reads each locale's messages.gotext.json catalog under localesDir and writes a
// single combined HTML report to outputPath: one column per locale, one row per message id,
// and each locale's coverage. Rows every locale renders identically to English are left out.
func Generate(localesDir string, outputPath string, u *ui.UI) error {
	if err := validateConfig(localesDir, outputPath); err != nil {
		return err
	}

	directories, err := catalog.LocaleDirs(localesDir)
	if err != nil {
		return err
	}

	// locale -> id -> translation
	translations := make(map[string]map[string]string)
	// id -> English message text (from en-US "message" field)
	englishText := make(map[string]string)
	// all known ids across every locale
	ids := make(map[string]struct{})

	for _, directory := range directories {
		locale := filepath.Base(directory)
		catalogPath := filepath.Join(directory, catalog.MessagesFile)
		cat, err := catalog.Load(catalogPath)
		if err != nil {
			return err
		}

		if cat.Language != locale {
			u.Warn("%s declares language %q but was loaded from locale directory %q", catalogPath, cat.Language, locale)
		}

		for _, msg := range cat.Messages {
			ids[string(msg.ID)] = struct{}{}
		}

		if locale == catalog.BaseLocale {
			for _, msg := range cat.Messages {
				englishText[string(msg.ID)] = msg.Message
			}
			continue
		}

		byID := make(map[string]string, len(cat.Messages))
		for _, msg := range cat.Messages {
			translation, ok := msg.Text()
			if !ok && !msg.Untranslated() {
				u.Warn("%s has a non-string translation for %q; rendering as %s", catalogPath, msg.ID, nonStringTranslationMarker)
				translation = nonStringTranslationMarker
			}
			byID[string(msg.ID)] = translation
		}
		translations[locale] = byID
	}

	if len(translations) == 0 {
		return fmt.Errorf("no non-%s locale catalogs found in %s", catalog.BaseLocale, localesDir)
	}

	locales := slices.Sorted(maps.Keys(translations))
	sortedIDs := slices.Sorted(maps.Keys(ids))

	// Coverage counts the base locale's ids, so an id removed from the source but left in one
	// locale doesn't lower every other locale's score.
	sourceIDs := englishText
	if len(sourceIDs) == 0 {
		sourceIDs = make(map[string]string, len(ids))
		for id := range ids {
			sourceIDs[id] = ""
		}
	}
	data := reportData{Locales: make([]localeCoverage, len(locales)), Total: len(sourceIDs)}
	for i, locale := range locales {
		data.Locales[i] = localeCoverage{Name: locale, Total: len(sourceIDs)}
	}
	for _, id := range sortedIDs {
		english := englishText[id]
		if english == "" {
			english = id
		}
		row := reportRow{English: english, Cells: make([]reportCell, len(locales))}
		allMatch := true
		for i, locale := range locales {
			text := translations[locale][id]
			missing := text == ""
			if _, source := sourceIDs[id]; source && !missing {
				data.Locales[i].Translated++
			}
			row.Cells[i] = reportCell{Text: text, Missing: missing}
			row.Missing = row.Missing || missing
			if text != english {
				allMatch = false
			}
		}
		if allMatch {
			continue
		}
		if row.Missing {
			data.MissingRows++
		}
		data.Rows = append(data.Rows, row)
	}

	if err := writeReport(outputPath, data); err != nil {
		return err
	}

	u.Success("Wrote %s (%d locales, %d rows)", outputPath, len(locales), len(data.Rows))
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
