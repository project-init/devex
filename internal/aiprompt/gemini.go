package aiprompt

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/manifoldco/promptui"
)

func RunGemini(prompts map[string]Prompt) error {
	prompt, err := selectAiPrompt(prompts)
	if err != nil {
		return err
	}

	arguments := make([]any, len(prompt.Args))
	for index, arg := range prompt.Args {
		argument, err := promptForArgument(arg)
		if err != nil {
			return err
		}
		arguments[index] = argument
	}

	promptString := fmt.Sprintf(prompt.Template, arguments...)
	cmd := exec.Command("gemini", "-i", promptString)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	err = cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}

	return nil
}

func selectAiPrompt(prompts map[string]Prompt) (Prompt, error) {
	promptNames := make([]string, 0)
	for name := range prompts {
		promptNames = append(promptNames, name)
	}

	uiPrompt := promptui.Select{
		Label: "Select AI Query",
		Items: promptNames,
	}

	_, name, err := uiPrompt.Run()
	if err != nil {
		return Prompt{}, err
	}

	return prompts[name], nil
}

func promptForArgument(arg Arg) (string, error) {
	if len(arg.Options) == 0 {
		argPrompt := promptui.Prompt{
			Label: arg.Query,
		}
		argument, err := argPrompt.Run()
		if err != nil {
			return "", err
		}
		return argument, err
	}

	argPrompt := promptui.Select{
		Label: arg.Query,
		Items: arg.Options,
	}

	_, argument, err := argPrompt.Run()
	if err != nil {
		return "", err
	}

	return argument, nil
}
