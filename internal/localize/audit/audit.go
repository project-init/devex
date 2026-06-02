package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	baseLocale          = "en-US"
	messagesCatalogName = "messages.gotext.json"
	outCatalogName      = "out.gotext.json"
)

type FindingKind string

type catalog struct {
	Language string    `json:"language"`
	Messages []message `json:"messages"`
}

type message struct {
	ID          string `json:"id"`
	Translation string `json:"translation"`
}

func loadCatalog(path string) (*catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cat catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, err
	}
	return &cat, nil
}

func audit(localesDir string) error {
	missingTranslations := make([]string, 0)
	uncopied := make([]string, 0)

	err := filepath.Walk(localesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		switch filepath.Base(path) {
		case messagesCatalogName:
			c, err := loadCatalog(path)
			if err != nil {
				return err
			}

			// translations expected to be empty here
			if c.Language != baseLocale {
				return nil
			}

			for _, m := range c.Messages {
				if m.Translation == "" {
					missingTranslations = append(missingTranslations, fmt.Sprintf("  [%s} %v", c.Language, m.ID))
				}
			}

		case outCatalogName:
			out, err := loadCatalog(path)
			if err != nil {
				return err
			}

			messagesPath := filepath.Join(filepath.Dir(path), messagesCatalogName)
			msgs, err := loadCatalog(messagesPath)
			if err != nil {
				return err
			}

			known := make(map[string]struct{}, len(msgs.Messages))
			for _, m := range msgs.Messages {
				known[m.ID] = struct{}{}
			}

			for _, m := range out.Messages {
				if _, ok := known[m.ID]; !ok {
					uncopied = append(uncopied, fmt.Sprintf("  [%s] %v", out.Language, m.ID))
				}
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	failed := false

	if len(missingTranslations) > 0 {
		failed = true
		_, _ = fmt.Fprintf(os.Stderr, "%d missing translations:\n", len(missingTranslations))
		for _, m := range missingTranslations {
			_, _ = fmt.Fprintf(os.Stderr, "  %s\n", m)
		}
	}

	if len(uncopied) > 0 {
		failed = true
		_, _ = fmt.Fprintf(os.Stderr, "%d Messages in out.gotext.json not copied in messages.gotext.json:\n", len(uncopied))
		for _, m := range uncopied {
			_, _ = fmt.Fprintf(os.Stderr, "  %s\n", m)
		}
	}

	if failed {
		return errors.New("audit failed")
	}

	fmt.Println("All translations are present.")
	return nil
}
