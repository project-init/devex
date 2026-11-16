package jira

import (
	"sync"

	"github.com/project-init/gommon/pkg/jiraclient"
)

// linkCache remembers each issue's inward links, so apply asks Jira once per issue. Apply runs
// operations side by side, so every access takes the lock; nothing else touches the map.
type linkCache struct {
	mu    sync.Mutex
	links map[string]map[string]bool // issue key → link keys
}

// lookup reports whether issueKey has the link, and whether its links are cached at all.
func (c *linkCache) lookup(issueKey, linkType, blockingKey string) (found, cached bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	links, cached := c.links[issueKey]
	return links[linkKey(linkType, blockingKey)], cached
}

// store merges links into issueKey's cache rather than replacing it, so a snapshot read before
// another worker linked the issue cannot drop that link.
func (c *linkCache) store(issueKey string, links []jiraclient.IssueLink) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.links == nil {
		c.links = map[string]map[string]bool{}
	}
	if c.links[issueKey] == nil {
		c.links[issueKey] = make(map[string]bool, len(links))
	}
	for _, link := range links {
		if link.InwardIssue != "" {
			c.links[issueKey][linkKey(link.Type, link.InwardIssue)] = true
		}
	}
}

func (c *linkCache) add(issueKey, linkType, blockingKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.links == nil {
		c.links = map[string]map[string]bool{}
	}
	if c.links[issueKey] == nil {
		c.links[issueKey] = map[string]bool{}
	}
	c.links[issueKey][linkKey(linkType, blockingKey)] = true
}

func (c *linkCache) forget(issueKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.links, issueKey)
}
