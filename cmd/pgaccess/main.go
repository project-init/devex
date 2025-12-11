package main

import (
	"fmt"
	"os"

	"github.com/project-init/devex/internal/pgaccess"
)

func main() {
	if len(os.Args) != 3 {
		usage()
	}

	pgAccessFile := os.Args[1]
	environment := os.Args[2]

	psqlCfg, err := pgaccess.LoadPGAccessEnvironmentUser(pgAccessFile, environment)
	if err != nil {
		panic(err)
	}

	err = pgaccess.RunPsql(psqlCfg)
	if err != nil {
		panic(err)
	}
}

func usage() {
	fmt.Println("Usage: pgaccess <pgAccessFilePath> <environment>")
	os.Exit(1)
}
