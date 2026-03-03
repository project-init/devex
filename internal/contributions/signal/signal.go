package signal

import (
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

	err = getUserSignal(cfg, allPrs)
	if err != nil {
		return err
	}

	getRepoSignal(cfg, allPrs)
	return nil
}
