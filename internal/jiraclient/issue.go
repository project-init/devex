package jiraclient

import (
	"context"
	"net/url"
)

// IssueResponse is a basic representation of a created / fetched Jira issue
type IssueResponse struct {
	ID 	string `json:"id"`
	Key string `json:"key"`
}

// SearchResult is a response from JQL search
type SearchResult struct {
	Issues 			[]IssueResponse `json:"issues"`
	NextPageToken 	string 
	IsLast 			bool
}

// SearchJQL executes a JQL query and returns SearchResult
func (c *Client) SearchJQL(ctx context.Context, jql string, maxResults int, nextPageToken string) (*SearchResult, error) {
	query := url.Values{}
	query.Set("jql", jql)
	query.Set("fields", "key")

	if maxResults > 0 {
		//set to 100 to match old behavior
		query.Set("maxResults", "100")
	}
	if nextPageToken != "" {
		query.Set("nextPageToken", nextPageToken)
	}

	req, err := c.NewRequest(ctx, "GET", "/rest/api/3/search/jql?"+query.Encode(), nil)
	
	if err != nil {
		return nil, err
	}

	var result SearchResult
	if err := c.Do(req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetIssueProperty retrieves a property of an issue
func (c *Client) GetIssueProperty(ctx context.Context, issueKey, propertyKey string) (string, error) {
	req, err := c.NewRequest(ctx, "GET", "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/properties/"+url.PathEscape(propertyKey), nil)
	if err != nil {
		return "", err
	}
	
	var property struct {
		Value struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	
	if err := c.Do(req, &property); err != nil {
		return "", err // Will return httpStatusError if 404 Not Found
	}
	return property.Value.ID, nil
}

// CreateIssue creates a new issue in Jira
func (c *Client) CreateIssue(ctx context.Context, body map[string]any) (*IssueResponse, error) {
	req, err := c.NewRequest(ctx, "POST", "/rest/api/3/issue", body)
	if err != nil {
		return nil, err
	}
	
	var response IssueResponse
	if err := c.Do(req, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// CreateIssueLink creates a link between two issues
func (c *Client) CreateIssueLink(ctx context.Context, linkType, inwardIssueKey, outwardIssueKey string) error {
	body := map[string]any{
		"type":         map[string]string{"name": linkType},
		"inwardIssue":  map[string]string{"key": inwardIssueKey},
		"outwardIssue": map[string]string{"key": outwardIssueKey},
	}
	
	req, err := c.NewRequest(ctx, "POST", "/rest/api/3/issueLink", body)
	if err != nil {
		return err
	}
	
	return c.Do(req, nil)
}

// IssueLink represents an existing link
type IssueLink struct {
	Type        string
	InwardIssue string
}

// GetIssueLinks fetches an issue to get its links (used by our Adapter for checking if a link exists)
func (c *Client) GetIssueLinks(ctx context.Context, issueKey string) ([]IssueLink, error) {
	req, err := c.NewRequest(ctx, "GET", "/rest/api/3/issue/"+url.PathEscape(issueKey)+"?fields=issuelinks", nil)
	if err != nil {
		return nil, err
	}
	
	var issue struct {
		Fields struct {
			IssueLinks []struct {
				Type struct {
					Name string `json:"name"`
				} `json:"type"`
				InwardIssue struct {
					Key string `json:"key"`
				} `json:"inwardIssue"`
			} `json:"issuelinks"`
		} `json:"fields"`
	}
	
	if err := c.Do(req, &issue); err != nil {
		return nil, err
	}
	
	var links []IssueLink
	for _, l := range issue.Fields.IssueLinks {
		if l.InwardIssue.Key != "" {
			links = append(links, IssueLink{Type: l.Type.Name, InwardIssue: l.InwardIssue.Key})
		}
	}
	return links, nil
}

// GetIssueLinkTypes fetches all valid link types on the Jira instance
func (c *Client) GetIssueLinkTypes(ctx context.Context) ([]string, error) {
	req, err := c.NewRequest(ctx, "GET", "/rest/api/3/issueLinkType", nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		IssueLinkTypes []struct {
			Name string `json:"name"`
		} `json:"issueLinkTypes"`
	}
	if err := c.Do(req, &response); err != nil {
		return nil, err
	}
	var names []string
	for _, t := range response.IssueLinkTypes {
		names = append(names, t.Name)
	}
	return names, nil
}
