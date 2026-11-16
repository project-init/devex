// Package catalog reads gotext message catalogs: one directory per locale under a locales
// directory, each holding messages.gotext.json and out.gotext.json.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const (
	// BaseLocale is the source locale every other locale translates from.
	BaseLocale = "en-US"
	// MessagesFile is a locale's curated catalog, where translators write translations.
	MessagesFile = "messages.gotext.json"
	// OutFile is the catalog gotext extracts from source on each run.
	OutFile = "out.gotext.json"
)

// Catalog is one locale's gotext catalog.
type Catalog struct {
	Language string    `json:"language"`
	Messages []Message `json:"messages"`
}

// Message is one catalog entry. Translation is a string, or an object for plural and select
// messages.
type Message struct {
	ID          ID     `json:"id"`
	Message     string `json:"message"`
	Translation any    `json:"translation"`
}

// Text returns the translation when it is a string.
func (m Message) Text() (string, bool) {
	text, ok := m.Translation.(string)
	return text, ok
}

// Untranslated reports whether the message has no translation, or an empty one.
func (m Message) Untranslated() bool {
	text, isText := m.Text()
	return m.Translation == nil || isText && text == ""
}

// placeholderPattern matches a gotext placeholder such as {Name}.
var placeholderPattern = regexp.MustCompile(`\{[A-Za-z0-9_]+\}`)

// Placeholders returns the placeholders in text, such as {Name}, in order of appearance.
func Placeholders(text string) []string {
	return placeholderPattern.FindAllString(text, -1)
}

// ID is a message id. gotext writes it as a string or as a [key, fallback] array; ID keeps the
// first element.
type ID string

// UnmarshalJSON accepts both id forms.
func (id *ID) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*id = ID(text)
		return nil
	}
	var parts []string
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("gotext id array is empty")
	}
	*id = ID(parts[0])
	return nil
}

// Load reads the catalog at path.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("missing %s: %w", path, os.ErrNotExist)
		}
		return nil, err
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &catalog, nil
}

// LocaleDirs returns the sorted locale directories under localesDir, following symlinks. It
// skips files, such as a stray .DS_Store, and hidden directories.
func LocaleDirs(localesDir string) ([]string, error) {
	entries, err := os.ReadDir(localesDir)
	if err != nil {
		return nil, fmt.Errorf("read locales directory: %w", err)
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			continue
		}
		path := filepath.Join(localesDir, entry.Name())
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		dirs = append(dirs, path)
	}
	sort.Strings(dirs)
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no locale directories found in %s", localesDir)
	}
	return dirs, nil
}
