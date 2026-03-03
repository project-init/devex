package signal

import (
	"fmt"
	"os"
	"time"

	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

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
