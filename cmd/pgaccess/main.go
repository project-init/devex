package main

import (
	"fmt"
	"os"

	"github.com/manifoldco/promptui"
	"github.com/project-init/devex/internal/pgaccess"
)

func main() {
	if len(os.Args) > 2 {
		usage()
	}

	pgAccessFile := ".pgaccess"
	if len(os.Args) > 1 {
		pgAccessFile = os.Args[1]
	}

	fmt.Printf("Using %s as the pgaccess file...\n", pgAccessFile)

	environments, err := pgaccess.LoadPGAccessEnvironments(pgAccessFile)
	if err != nil {
		panic(err)
	}

	environment, err := selectEnvironment(environments)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%s chosen. Loading environment config...\n", environment)
	environmentConfig, found := environments[environment]
	if !found {
		panic(fmt.Errorf("environment %s not found in file %s", environment, pgAccessFile))
	}

	psqlCfg, err := pgaccess.LoadPGAccessEnvironment(environmentConfig)
	if err != nil {
		panic(err)
	}

	err = pgaccess.RunPsql(psqlCfg)
	if err != nil {
		panic(err)
	}
}

func selectEnvironment(environments map[string]pgaccess.EnvironmentConfig) (string, error) {
	environmentNames := make([]string, 0, len(environments))
	for environmentName := range environments {
		environmentNames = append(environmentNames, environmentName)
	}

	sort.Strings(environmentNames)

	argPrompt := promptui.Select{
		Label: "Select Environment",
		Items: environmentNames,
	}

	_, argument, err := argPrompt.Run()
	if err != nil {
		return "", err
	}

	return argument, nil
}

func usage() {
	fmt.Println("Usage: pgaccess || pgaccess <pgAccessFilePath>")
	os.Exit(1)
}
