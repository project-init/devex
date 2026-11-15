package signal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jszwec/csvutil"
	"github.com/project-init/devex/internal/contributions/config"
)

// writeSignals writes signals as a dated CSV of kind in the signals directory, creating the
// directory when missing, and returns the file's path.
func writeSignals(cfg *config.Config, kind string, signals any) (string, error) {
	if err := os.MkdirAll(cfg.OutputDirectories.Signals, 0o755); err != nil {
		return "", err
	}
	year, month, day := time.Now().Date()
	path := filepath.Join(cfg.OutputDirectories.Signals, fmt.Sprintf("%d_%02d_%02d_%s_%d_days_signal.csv", year, month, day, kind, cfg.NumLookBackDays))
	bytes, err := csvutil.Marshal(signals)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, bytes, 0o644)
}
