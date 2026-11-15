package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/project-init/devex/internal/localize/catalog"
	"github.com/project-init/devex/internal/ui"
)

// audit reports base-locale messages without a translation, and messages gotext extracted into
// out.gotext.json that nobody copied into messages.gotext.json. It skips directories without a
// messages.gotext.json, such as helper directories beside the locales.
func audit(localesDir string, u *ui.UI) error {
	dirs, err := catalog.LocaleDirs(localesDir)
	if err != nil {
		return err
	}
	var missing, uncopied []string
	for _, dir := range dirs {
		messages, err := catalog.Load(filepath.Join(dir, catalog.MessagesFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if messages.Language == catalog.BaseLocale {
			for _, message := range messages.Messages {
				if message.Untranslated() {
					missing = append(missing, fmt.Sprintf("[%s] %s", messages.Language, message.ID))
				}
			}
		}

		out, err := catalog.Load(filepath.Join(dir, catalog.OutFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		known := make(map[catalog.ID]struct{}, len(messages.Messages))
		for _, message := range messages.Messages {
			known[message.ID] = struct{}{}
		}
		for _, message := range out.Messages {
			if _, ok := known[message.ID]; !ok {
				uncopied = append(uncopied, fmt.Sprintf("[%s] %s", out.Language, message.ID))
			}
		}
	}

	printFindings(u, fmt.Sprintf("Missing translations (%d):", len(missing)), missing)
	printFindings(u, fmt.Sprintf("Messages in %s not copied into %s (%d):", catalog.OutFile, catalog.MessagesFile, len(uncopied)), uncopied)
	if len(missing) > 0 || len(uncopied) > 0 {
		return fmt.Errorf("localize audit found %d missing and %d uncopied messages", len(missing), len(uncopied))
	}
	u.Success("All translations are present.")
	return nil
}

func printFindings(u *ui.UI, heading string, findings []string) {
	if len(findings) == 0 {
		return
	}
	u.Printf("%s\n", u.Heading(heading))
	for _, finding := range findings {
		u.Printf("  %s\n", finding)
	}
}
