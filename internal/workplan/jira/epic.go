package jira

import (
	"context"
	"fmt"
)

type jiraEpic struct {
	ProjectKey  string
	Summary     string
	Description string
	ParentKey   *string
	Labels      []string
}

func (h *Http) createEpic(ctx context.Context, epic *jiraEpic) (*Response, error) {
	jiraRequest := Request{
		Fields: map[string]interface{}{
			"project":     map[string]string{"key": epic.ProjectKey},
			"issuetype":   map[string]string{"name": "Epic"},
			"summary":     epic.Summary,
			"description": epic.Description,
			"labels":      epic.Labels,
		},
	}

	if epic.ParentKey != nil {
		jiraRequest.Fields["parent"] = map[string]string{"key": *epic.ParentKey}
	}

	req, err := h.newRequest(ctx, "POST", "/rest/api/2/issue", jiraRequest)
	if err != nil {
		return nil, err
	}

	var response Response
	_, err = h.do(req, &response)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to create epic", err)
	}
	return &response, nil
}
