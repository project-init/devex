package jira

type Response struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Self string `json:"self"`
}

type Request struct {
	Fields map[string]interface{} `json:"fields"`
}
