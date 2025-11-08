package workplan

import (
	"context"
	"os"

	"github.com/project-init/devex/internal/workplan/jira"
	"github.com/project-init/devex/internal/workplan/models"
	"gopkg.in/yaml.v3"
)

func PublishWorkPlanToJira(ctx context.Context, workplanPath string) error {
	wp, err := loadWorkplan(workplanPath)
	if err != nil {
		return err
	}

	jiraClient, err := jira.New()
	if err != nil {
		return err
	}

	if err = jiraClient.Create(ctx, wp); err != nil {
		return err
	}

	return nil
}

func loadWorkplan(workplanPath string) (*models.Workplan, error) {
	bytes, err := os.ReadFile(workplanPath)
	if err != nil {
		return nil, err
	}

	var workplan models.Workplan
	if err = yaml.Unmarshal(bytes, &workplan); err != nil {
		return nil, err
	}
	return &workplan, nil
}
