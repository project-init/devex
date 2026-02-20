package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/project-init/devex/internal/contributions"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/gh"
)

func main() {
	cfg, err := config.NewConfigFromYaml(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("starting Contribution Collections At - %s\n", time.Now().String())
	github, err := gh.New(cfg.Organization)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	repos, err := github.GetRepos(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Got %d repositories:\n", len(repos))
	for _, repo := range repos {
		log.Printf("\t%s", repo)
	}

	year, month, day := time.Now().AddDate(0, 0, -cfg.NumLookBackDays).Date()
	cutoffDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	prs := github.GetPRs(ctx, cutoffDate, repos, cfg)
	contributions.GetGHSignal(cfg, prs)
	log.Printf("completed Collecting Contributions At - %s\n", time.Now().String())
}
