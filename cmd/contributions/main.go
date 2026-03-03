package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"time"

	"github.com/project-init/devex/internal/contributions/collection"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/signal"
)

var validCommands = []string{
	"collect",
	"signal",
}

func main() {
	if len(os.Args) != 3 {
		usage()
	}

	cmd := os.Args[1]
	cfg, err := config.NewConfigFromYaml(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}

	if !slices.Contains(validCommands, cmd) {
		log.Fatalf("unknown command %s", cmd)
	}

	log.Printf("starting Contributions (%s) At - %s\n", cmd, time.Now().String())
	ctx := context.Background()
	err = fmt.Errorf("unimplemented command %s", cmd)
	switch cmd {
	case "collect":
		err = collection.Run(ctx, cfg)
	case "signal":
		err = signal.Run(cfg)
	}

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("completed Contributions (%s) At - %s\n", cmd, time.Now().String())
}

func usage() {
	usageString := fmt.Sprintf("Usage: %s <cmd> <config_file>\n", os.Args[0])
	usageString += "\nCommands:\n"
	usageString += "\tcollect - Gather all pull requests per the definition in the config file and store them in the prs directory.\n"
	usageString += "\tsignal - Create a signal output per the definition in the config file and store it in the signals directory.\n"
	usageString += "\n"
	log.Fatal(usageString)
}
