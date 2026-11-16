package signal

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/project-init/devex/internal/contributions/collection"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/types"
)

// Run writes the user and repository signals for the pull requests collect stored, and returns
// the files' paths.
func Run(cfg *config.Config) ([]string, error) {
	existingPrs, err := collection.GetExistingPRs(cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no collected pull requests in %s; run devex contributions collect first", cfg.OutputDirectories.Prs)
	}
	if err != nil {
		return nil, err
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

	userPath, err := getUserSignal(cfg, allPrs)
	if err != nil {
		return nil, err
	}
	repoPath, err := getRepoSignal(cfg, allPrs)
	if err != nil {
		return nil, err
	}
	return []string{userPath, repoPath}, nil
}
