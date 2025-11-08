package jira

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/project-init/devex/internal/workplan/models"
)

type jiraTask struct {
	ProjectKey  string
	Summary     string
	Description string
	IssueType   string
	Labels      []string
	EpicKey     string
}

func (h *Http) createTasks(ctx context.Context, tasks []models.Task, epicKey string, projectKey string, epicLabels []string) error {
	for _, task := range tasks {
		labels := append(task.Labels, epicLabels...)
		err := h.addTaskToEpic(ctx, &jiraTask{
			ProjectKey:  projectKey,
			Summary:     task.Summary,
			Description: task.Description,
			IssueType:   getType(task.Type),
			Labels:      labels,
			EpicKey:     epicKey,
		})
		if err != nil {
			return fmt.Errorf("%w: failed to add task to epic (%s)", err, epicKey)
		}
	}
	return nil
}

func getType(baseType string) string {
	switch strings.ToLower(baseType) {
	case "task":
		return "task"
	case "bug":
		return "bug"
	case "feature":
		return "feature"
	default:
		log.Printf("unknown task type: %s, using 'task' instead", baseType)
		return "task"
	}
}

func (h *Http) addTaskToEpic(ctx context.Context, task *jiraTask) error {
	jiraRequest := Request{
		Fields: map[string]interface{}{
			"project":     map[string]string{"key": task.ProjectKey},
			"issuetype":   map[string]string{"name": task.IssueType},
			"priority":    map[string]string{"name": "Medium"},
			"summary":     task.Summary,
			"description": task.Description,
			"labels":      task.Labels,
			"parent":      map[string]string{"key": task.EpicKey},
		},
	}
	log.Print(jiraRequest)
	req, err := h.newRequest(ctx, "POST", "/rest/api/2/issue", jiraRequest)
	if err != nil {
		return err
	}

	var response Response
	_, err = h.do(req, &response)
	if err != nil {
		return err
	}
	return nil
}
