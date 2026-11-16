package gh

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/contributions/types"
)

var invalidLogins = []string{
	"github-actions[bot]",
}

// reviewFields selects a review connection's page and each review's author and state.
const reviewFields = `pageInfo { hasNextPage endCursor }
          nodes { author { __typename login } state }`

// pullRequestsQuery lists a repository's merged pull requests, most recently updated first, with
// their first page of reviews. A merge updates a pull request, so once a page reaches one updated
// before the cutoff, no later page holds a merge after it.
const pullRequestsQuery = `query($owner: String!, $repo: String!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(states: MERGED, first: 50, after: $after, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        createdAt
        mergedAt
        updatedAt
        author { __typename login }
        reviews(first: 100) {
          ` + reviewFields + `
        }
      }
    }
  }
}`

// reviewsQuery reads one pull request's reviews after the first page.
const reviewsQuery = `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviews(first: 100, after: $after) {
        ` + reviewFields + `
      }
    }
  }
}`

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type actor struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

// name is the actor's login as REST reports it: GraphQL drops the [bot] suffix that the signal's
// bot check and earlier collections rely on. A deleted account has no actor and shows as ghost,
// as on GitHub.
func (a *actor) name() string {
	switch {
	case a == nil:
		return "ghost"
	case a.Typename == "Bot" && !strings.HasSuffix(a.Login, "[bot]"):
		return a.Login + "[bot]"
	default:
		return a.Login
	}
}

type reviewConnection struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []struct {
		Author *actor `json:"author"`
		State  string `json:"state"`
	} `json:"nodes"`
}

type pullRequestNode struct {
	Number    int              `json:"number"`
	CreatedAt time.Time        `json:"createdAt"`
	MergedAt  time.Time        `json:"mergedAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Author    *actor           `json:"author"`
	Reviews   reviewConnection `json:"reviews"`
}

type pullRequestsResponse struct {
	Repository *struct {
		PullRequests struct {
			PageInfo pageInfo          `json:"pageInfo"`
			Nodes    []pullRequestNode `json:"nodes"`
		} `json:"pullRequests"`
	} `json:"repository"`
}

type reviewsResponse struct {
	Repository *struct {
		PullRequest *struct {
			Reviews reviewConnection `json:"reviews"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// GetRepoPRs returns repo's pull requests merged after cutoff, with every review, whenever they
// were opened.
func (g *GH) GetRepoPRs(ctx context.Context, cutoff time.Time, repo string) ([]types.PR, error) {
	prs, err := g.mergedSince(ctx, repo, cutoff)
	if err != nil {
		return nil, fmt.Errorf("get pull requests for %s: %w", repo, err)
	}
	return prs, nil
}

// catchUpMargin covers clock skew between this machine and GitHub when choosing where the
// catch-up pass stops.
const catchUpMargin = 10 * time.Minute

func (g *GH) mergedSince(ctx context.Context, repo string, cutoff time.Time) ([]types.PR, error) {
	started := time.Now()
	seen := make(map[int]bool)
	found, pages, err := g.scan(ctx, repo, cutoff, cutoff, seen, make([]types.PR, 0))
	if err != nil || pages == 1 {
		return found, err
	}
	// A page cursor is a position in an order that updates change, so a pull request updated
	// mid-scan jumps behind the cursor and the scan misses it. Whatever jumped was updated after
	// the scan began, so one more pass over those pull requests finds it.
	stopAt := started.Add(-catchUpMargin)
	if stopAt.Before(cutoff) {
		stopAt = cutoff
	}
	found, _, err = g.scan(ctx, repo, cutoff, stopAt, seen, found)
	return found, err
}

// scan appends to found each pull request merged after cutoff and absent from seen, paging until
// a page reaches one updated at or before stopAt. It also returns how many pages it read.
func (g *GH) scan(ctx context.Context, repo string, cutoff, stopAt time.Time, seen map[int]bool, found []types.PR) ([]types.PR, int, error) {
	variables := map[string]any{"owner": g.organization, "repo": repo}
	for pages := 1; ; pages++ {
		var response pullRequestsResponse
		if err := g.graphQL(ctx, pullRequestsQuery, variables, &response); err != nil {
			return nil, 0, err
		}
		if response.Repository == nil {
			return nil, 0, fmt.Errorf("repository %s/%s not found", g.organization, repo)
		}
		page := response.Repository.PullRequests
		for _, node := range page.Nodes {
			author := node.Author.name()
			if seen[node.Number] || !node.MergedAt.After(cutoff) || slices.Contains(invalidLogins, author) {
				continue
			}
			seen[node.Number] = true
			reviews, err := g.allReviews(ctx, repo, node)
			if err != nil {
				return nil, 0, err
			}
			found = append(found, types.PR{
				Author:      author,
				TimeToMerge: node.MergedAt.Sub(node.CreatedAt),
				MergedAt:    node.MergedAt,
				Number:      node.Number,
				Repo:        repo,
				Reviews:     strings.Join(reviews, "!"),
			})
		}
		if !page.PageInfo.HasNextPage || len(page.Nodes) == 0 || !page.Nodes[len(page.Nodes)-1].UpdatedAt.After(stopAt) {
			return found, pages, nil
		}
		variables["after"] = page.PageInfo.EndCursor
	}
}

// allReviews returns node's reviews as login:state, reading the pages after the first.
func (g *GH) allReviews(ctx context.Context, repo string, node pullRequestNode) ([]string, error) {
	connection := node.Reviews
	var reviews []string
	for {
		for _, review := range connection.Nodes {
			reviews = append(reviews, review.Author.name()+":"+review.State)
		}
		if !connection.PageInfo.HasNextPage {
			return reviews, nil
		}
		var response reviewsResponse
		variables := map[string]any{"owner": g.organization, "repo": repo, "number": node.Number, "after": connection.PageInfo.EndCursor}
		if err := g.graphQL(ctx, reviewsQuery, variables, &response); err != nil {
			return nil, err
		}
		if response.Repository == nil || response.Repository.PullRequest == nil {
			return nil, fmt.Errorf("pull request %s#%d not found", repo, node.Number)
		}
		connection = response.Repository.PullRequest.Reviews
	}
}
