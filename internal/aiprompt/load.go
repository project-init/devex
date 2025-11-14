package aiprompt

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadPrompts(promptDirectory string) (map[string]Prompt, error) {
	entries, err := os.ReadDir(promptDirectory)
	if err != nil {
		return nil, err
	}

	prompts := make(map[string]Prompt)
	for _, dirEntry := range entries {
		if !strings.HasSuffix(dirEntry.Name(), ".prompt") || dirEntry.IsDir() {
			continue
		}

		promptName := strings.ReplaceAll(dirEntry.Name(), ".prompt", "")
		if _, found := prompts[promptName]; found {
			return nil, fmt.Errorf("found prompt with the same name as an existing prompt: %s", promptName)
		}

		bytes, err := os.ReadFile(path.Join(promptDirectory, dirEntry.Name()))
		if err != nil {
			return nil, err
		}

		var prompt Prompt
		if err = yaml.Unmarshal(bytes, &prompt); err != nil {
			return nil, err
		}
		prompts[promptName] = prompt
	}
	return prompts, nil
}
