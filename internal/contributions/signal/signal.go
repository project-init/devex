package signal

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/collection"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

func Run(cfg *config.Config) error {
	existingPrs, err := collection.GetExistingPRs(cfg)
	if err != nil {
		return err
	}

	allPrs := make([]types.PR, 0)
	cutoffDate := cfg.CutoffDate()
	for _, repoPrs := range existingPrs {
		for _, pr := range repoPrs {
			if pr.MergedAt.Before(cutoffDate) {
				continue
			}

			allPrs = append(allPrs, pr)
		}
	}

	getUserSignal(cfg, allPrs)
	getRepoSignal(cfg, allPrs)
	return nil
}

func isDependabot(author string) bool {
	return strings.Contains(author, "[bot]")
}

func authorMultiplier(author string) float64 {
	if isDependabot(author) {
		return 0.01
	}
	return 1.0
}

func signalsToCSVUsers(signals []*types.UserSignal, cfg *config.Config) error {
	year, month, day := time.Now().Date()
	f, err := os.Create(fmt.Sprintf("%s/%d_%02d_%02d_user_%d_days_signal.csv", cfg.OutputDirectories.Signals, year, month, day, cfg.NumLookBackDays))
	if err != nil {
		return err
	}

	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	bytes, err := csvutil.Marshal(signals)
	if err != nil {
		return err
	}

	_, err = f.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}

func signalsToCSVRepos(signals []*types.RepoSignal, cfg *config.Config) error {
	year, month, day := time.Now().Date()
	f, err := os.Create(fmt.Sprintf("%s/%d_%02d_%02d_repo_%d_days_signal.csv", cfg.OutputDirectories.Signals, year, month, day, cfg.NumLookBackDays))
	if err != nil {
		return err
	}

	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	bytes, err := csvutil.Marshal(signals)
	if err != nil {
		return err
	}

	_, err = f.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}
