package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/project-init/devex/internal/workplan/models"
)

type Http struct {
	client *http.Client
	url    string
	email  string
	apiKey string
}

func New() (*Http, error) {
	jiraUrl := os.Getenv("JIRA_URL")
	email := os.Getenv("JIRA_EMAIL")
	apiKey := os.Getenv("JIRA_API_KEY")

	if jiraUrl == "" || email == "" || apiKey == "" {
		return nil, fmt.Errorf("JIRA_URL or JIRA_EMAIL or JIRA_API_KEY environment variables not set")
	}

	return &Http{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		url:    jiraUrl,
		email:  email,
		apiKey: apiKey,
	}, nil
}

func (h *Http) newRequest(ctx context.Context, method string, urlPath string, body interface{}) (*http.Request, error) {
	var buf io.ReadWriter
	if body != nil {
		buf = &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, h.url+urlPath, buf)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(h.email, h.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return req, nil
}

func (h *Http) do(req *http.Request, i interface{}) (*http.Response, error) {
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 299 {
		body, _ := io.ReadAll(resp.Body)
		return resp, fmt.Errorf("unexpected status code %d (%s)", resp.StatusCode, string(body))
	}

	if i != nil {
		if err = json.NewDecoder(resp.Body).Decode(i); err != nil {
			return resp, fmt.Errorf("%w: error decoding response", err)
		}
	}
	return resp, nil
}

func (h *Http) Create(ctx context.Context, wp *models.Workplan) error {
	log.Println("Creating Epics and Tasks in JIRA")
	for _, epic := range wp.Epics {
		err := epic.CheckRequirements()
		if err != nil {
			return err
		}

		labels := append(epic.Labels, wp.Labels...)
		projectKey := wp.Project
		createdEpic, err := h.createEpic(ctx, &jiraEpic{
			ProjectKey:  projectKey,
			Labels:      labels,
			Description: epic.Description,
			Summary:     epic.Summary,
		})
		if err != nil {
			return fmt.Errorf("%w: failed to create epic (%s)", err, epic.Summary)
		}
		log.Printf("Created Epic at %s/browse/%s", h.url, createdEpic.Key)
		err = h.createTasks(ctx, epic.Tasks, createdEpic.Key, projectKey, labels)
		if err != nil {
			return fmt.Errorf("%w: failed to create tasks (%s)", err, epic.Summary)
		}
	}

	return nil
}
