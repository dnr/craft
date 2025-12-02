package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shurcooL/githubv4"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

// GitHubClient wraps the GitHub GraphQL client.
type GitHubClient struct {
	client *githubv4.Client
}

// NewGitHubClient creates a new GitHub GraphQL client with the given token.
func NewGitHubClient(token string) *GitHubClient {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(context.Background(), src)
	return &GitHubClient{client: githubv4.NewClient(httpClient)}
}

// getGitHubToken reads the GitHub token from GITHUB_TOKEN env var or gh CLI's keyring.
func getGitHubToken() (string, error) {
	// Try GITHUB_TOKEN first
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// TODO: get hostname from git remote config
	hostname := "github.com"

	// Read gh CLI config to get the username
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not get home directory: %w", err)
	}

	hostsPath := filepath.Join(home, ".config", "gh", "hosts.yml")
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return "", fmt.Errorf("no GITHUB_TOKEN and could not read gh config: %w", err)
	}

	var hosts map[string]struct {
		User       string `yaml:"user"`
		OAuthToken string `yaml:"oauth_token"`
	}
	if err := yaml.Unmarshal(data, &hosts); err != nil {
		return "", fmt.Errorf("could not parse gh config: %w", err)
	}

	hostConfig, ok := hosts[hostname]
	if !ok {
		return "", fmt.Errorf("no config for %s in gh hosts.yml", hostname)
	}

	// Try legacy oauth_token field first (older gh versions)
	if hostConfig.OAuthToken != "" {
		return hostConfig.OAuthToken, nil
	}

	// Try keyring (newer gh versions store token in secret storage)
	if hostConfig.User == "" {
		return "", fmt.Errorf("no user configured for %s in gh hosts.yml", hostname)
	}

	service := "gh:" + hostname
	token, err := keyring.Get(service, hostConfig.User)
	if err != nil {
		return "", fmt.Errorf("could not get token from keyring (service=%q, user=%q): %w", service, hostConfig.User, err)
	}

	return token, nil
}

// GraphQL response types for reuse across queries

type gqlPageInfo struct {
	HasNextPage githubv4.Boolean
	EndCursor   githubv4.String
}

type gqlActor struct {
	Login     githubv4.String
	AvatarURL githubv4.URI `graphql:"avatarUrl"`
	URL       githubv4.URI `graphql:"url"`
}

type gqlReviewComment struct {
	ID         githubv4.ID
	DatabaseID int64
	Body       githubv4.String
	CreatedAt  githubv4.DateTime
	UpdatedAt  githubv4.DateTime
	Author     gqlActor
	ReplyTo    struct {
		DatabaseID int64
	}
}

type gqlReviewThread struct {
	ID                githubv4.ID
	IsResolved        githubv4.Boolean
	IsOutdated        githubv4.Boolean
	Path              githubv4.String
	DiffSide          githubv4.String
	Line              githubv4.Int
	StartLine         *githubv4.Int
	OriginalLine      githubv4.Int
	OriginalStartLine *githubv4.Int
	SubjectType       githubv4.String
	Comments          struct {
		PageInfo gqlPageInfo
		Nodes    []gqlReviewComment
	} `graphql:"comments(first: 100)"`
}

type gqlIssueComment struct {
	ID         githubv4.ID
	DatabaseID int64
	Body       githubv4.String
	CreatedAt  githubv4.DateTime
	UpdatedAt  githubv4.DateTime
	Author     gqlActor
}

type gqlReview struct {
	ID          githubv4.ID
	DatabaseID  int64
	State       githubv4.String
	Body        githubv4.String
	SubmittedAt *githubv4.DateTime
	CreatedAt   githubv4.DateTime
	Author      gqlActor
}

// FetchPullRequest fetches all PR data including review threads, comments, and reviews.
// Handles pagination for all collections.
func (c *GitHubClient) FetchPullRequest(ctx context.Context, owner, repo string, number int) (*PullRequest, error) {
	// Initial query for PR metadata and first page of everything
	var prQuery struct {
		Repository struct {
			PullRequest struct {
				ID            githubv4.ID
				Number        githubv4.Int
				Title         githubv4.String
				Body          githubv4.String
				State         githubv4.String
				IsDraft       githubv4.Boolean
				BaseRefName   githubv4.String
				HeadRefName   githubv4.String
				BaseRefOid    githubv4.GitObjectID
				HeadRefOid    githubv4.GitObjectID
				Author        gqlActor
				ReviewThreads struct {
					PageInfo gqlPageInfo
					Nodes    []gqlReviewThread
				} `graphql:"reviewThreads(first: 100)"`
				Comments struct {
					PageInfo gqlPageInfo
					Nodes    []gqlIssueComment
				} `graphql:"comments(first: 100)"`
				Reviews struct {
					PageInfo gqlPageInfo
					Nodes    []gqlReview
				} `graphql:"reviews(first: 100)"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	vars := map[string]interface{}{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"number": githubv4.Int(number),
	}

	if err := c.client.Query(ctx, &prQuery, vars); err != nil {
		return nil, fmt.Errorf("GraphQL query failed: %w", err)
	}

	ghPR := prQuery.Repository.PullRequest

	// Collect all nodes, paginating as needed
	allThreads := ghPR.ReviewThreads.Nodes
	allIssueComments := ghPR.Comments.Nodes
	allReviews := ghPR.Reviews.Nodes

	// Paginate review threads
	if ghPR.ReviewThreads.PageInfo.HasNextPage {
		more, err := c.fetchAllReviewThreads(ctx, owner, repo, number, string(ghPR.ReviewThreads.PageInfo.EndCursor))
		if err != nil {
			return nil, err
		}
		allThreads = append(allThreads, more...)
	}

	// Paginate issue comments
	if ghPR.Comments.PageInfo.HasNextPage {
		more, err := c.fetchAllIssueComments(ctx, owner, repo, number, string(ghPR.Comments.PageInfo.EndCursor))
		if err != nil {
			return nil, err
		}
		allIssueComments = append(allIssueComments, more...)
	}

	// Paginate reviews
	if ghPR.Reviews.PageInfo.HasNextPage {
		more, err := c.fetchAllReviews(ctx, owner, repo, number, string(ghPR.Reviews.PageInfo.EndCursor))
		if err != nil {
			return nil, err
		}
		allReviews = append(allReviews, more...)
	}

	// Convert to our model
	pr := &PullRequest{
		ID:            string(ghPR.ID.(string)),
		Number:        int(ghPR.Number),
		Title:         string(ghPR.Title),
		Body:          string(ghPR.Body),
		State:         string(ghPR.State),
		IsDraft:       bool(ghPR.IsDraft),
		BaseRefName:   string(ghPR.BaseRefName),
		HeadRefName:   string(ghPR.HeadRefName),
		BaseRefOID:    string(ghPR.BaseRefOid),
		HeadRefOID:    string(ghPR.HeadRefOid),
		LastFetchedAt: time.Now(),
		Author:        convertActor(ghPR.Author),
	}

	// Convert review threads (with nested comment pagination)
	for _, t := range allThreads {
		thread, err := c.convertReviewThread(ctx, t)
		if err != nil {
			return nil, err
		}
		pr.ReviewThreads = append(pr.ReviewThreads, thread)
	}

	// Convert issue comments
	for _, c := range allIssueComments {
		pr.IssueComments = append(pr.IssueComments, convertIssueComment(c))
	}

	// Convert reviews
	for _, r := range allReviews {
		pr.Reviews = append(pr.Reviews, convertReview(r))
	}

	return pr, nil
}

// fetchAllReviewThreads paginates through remaining review threads
func (c *GitHubClient) fetchAllReviewThreads(ctx context.Context, owner, repo string, number int, cursor string) ([]gqlReviewThread, error) {
	var result []gqlReviewThread

	var query struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					PageInfo gqlPageInfo
					Nodes    []gqlReviewThread
				} `graphql:"reviewThreads(first: 100, after: $cursor)"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	for {
		vars := map[string]interface{}{
			"owner":  githubv4.String(owner),
			"name":   githubv4.String(repo),
			"number": githubv4.Int(number),
			"cursor": githubv4.String(cursor),
		}

		if err := c.client.Query(ctx, &query, vars); err != nil {
			return nil, fmt.Errorf("fetching review threads page: %w", err)
		}

		result = append(result, query.Repository.PullRequest.ReviewThreads.Nodes...)

		if !query.Repository.PullRequest.ReviewThreads.PageInfo.HasNextPage {
			break
		}
		cursor = string(query.Repository.PullRequest.ReviewThreads.PageInfo.EndCursor)
	}

	return result, nil
}

// FetchPRHead fetches just the current head OID of a PR (lightweight check).
func (c *GitHubClient) FetchPRHead(ctx context.Context, owner, repo string, number int) (string, error) {
	var query struct {
		Repository struct {
			PullRequest struct {
				HeadRefOID githubv4.GitObjectID `graphql:"headRefOid"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	vars := map[string]interface{}{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"number": githubv4.Int(number),
	}

	if err := c.client.Query(ctx, &query, vars); err != nil {
		return "", fmt.Errorf("fetching PR head: %w", err)
	}

	return string(query.Repository.PullRequest.HeadRefOID), nil
}

// fetchAllIssueComments paginates through remaining issue comments
func (c *GitHubClient) fetchAllIssueComments(ctx context.Context, owner, repo string, number int, cursor string) ([]gqlIssueComment, error) {
	var result []gqlIssueComment

	var query struct {
		Repository struct {
			PullRequest struct {
				Comments struct {
					PageInfo gqlPageInfo
					Nodes    []gqlIssueComment
				} `graphql:"comments(first: 100, after: $cursor)"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	for {
		vars := map[string]interface{}{
			"owner":  githubv4.String(owner),
			"name":   githubv4.String(repo),
			"number": githubv4.Int(number),
			"cursor": githubv4.String(cursor),
		}

		if err := c.client.Query(ctx, &query, vars); err != nil {
			return nil, fmt.Errorf("fetching issue comments page: %w", err)
		}

		result = append(result, query.Repository.PullRequest.Comments.Nodes...)

		if !query.Repository.PullRequest.Comments.PageInfo.HasNextPage {
			break
		}
		cursor = string(query.Repository.PullRequest.Comments.PageInfo.EndCursor)
	}

	return result, nil
}

// fetchAllReviews paginates through remaining reviews
func (c *GitHubClient) fetchAllReviews(ctx context.Context, owner, repo string, number int, cursor string) ([]gqlReview, error) {
	var result []gqlReview

	var query struct {
		Repository struct {
			PullRequest struct {
				Reviews struct {
					PageInfo gqlPageInfo
					Nodes    []gqlReview
				} `graphql:"reviews(first: 100, after: $cursor)"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	for {
		vars := map[string]interface{}{
			"owner":  githubv4.String(owner),
			"name":   githubv4.String(repo),
			"number": githubv4.Int(number),
			"cursor": githubv4.String(cursor),
		}

		if err := c.client.Query(ctx, &query, vars); err != nil {
			return nil, fmt.Errorf("fetching reviews page: %w", err)
		}

		result = append(result, query.Repository.PullRequest.Reviews.Nodes...)

		if !query.Repository.PullRequest.Reviews.PageInfo.HasNextPage {
			break
		}
		cursor = string(query.Repository.PullRequest.Reviews.PageInfo.EndCursor)
	}

	return result, nil
}

// fetchMoreThreadComments fetches additional comments for a thread via node query
func (c *GitHubClient) fetchMoreThreadComments(ctx context.Context, threadID string, cursor string) ([]gqlReviewComment, error) {
	var result []gqlReviewComment

	var query struct {
		Node struct {
			PullRequestReviewThread struct {
				Comments struct {
					PageInfo gqlPageInfo
					Nodes    []gqlReviewComment
				} `graphql:"comments(first: 100, after: $cursor)"`
			} `graphql:"... on PullRequestReviewThread"`
		} `graphql:"node(id: $id)"`
	}

	for {
		vars := map[string]interface{}{
			"id":     githubv4.ID(threadID),
			"cursor": githubv4.String(cursor),
		}

		if err := c.client.Query(ctx, &query, vars); err != nil {
			return nil, fmt.Errorf("fetching thread comments page: %w", err)
		}

		result = append(result, query.Node.PullRequestReviewThread.Comments.Nodes...)

		if !query.Node.PullRequestReviewThread.Comments.PageInfo.HasNextPage {
			break
		}
		cursor = string(query.Node.PullRequestReviewThread.Comments.PageInfo.EndCursor)
	}

	return result, nil
}

// convertReviewThread converts a GraphQL thread to our model, fetching more comments if needed
func (c *GitHubClient) convertReviewThread(ctx context.Context, t gqlReviewThread) (ReviewThread, error) {
	thread := ReviewThread{
		ID:           string(t.ID.(string)),
		Path:         string(t.Path),
		DiffSide:     DiffSide(t.DiffSide),
		Line:         int(t.Line),
		OriginalLine: int(t.OriginalLine),
		IsOutdated:   bool(t.IsOutdated),
		IsResolved:   bool(t.IsResolved),
		SubjectType:  SubjectType(t.SubjectType),
	}
	if t.StartLine != nil {
		sl := int(*t.StartLine)
		thread.StartLine = &sl
	}
	if t.OriginalStartLine != nil {
		osl := int(*t.OriginalStartLine)
		thread.OriginalStartLine = &osl
	}

	// Collect all comments, paginating if needed
	allComments := t.Comments.Nodes
	if t.Comments.PageInfo.HasNextPage {
		more, err := c.fetchMoreThreadComments(ctx, string(t.ID.(string)), string(t.Comments.PageInfo.EndCursor))
		if err != nil {
			return thread, err
		}
		allComments = append(allComments, more...)
	}

	for _, c := range allComments {
		thread.Comments = append(thread.Comments, convertReviewComment(c))
	}

	return thread, nil
}

// Conversion helpers

func convertActor(a gqlActor) Actor {
	return Actor{
		Login:     string(a.Login),
		AvatarURL: a.AvatarURL.String(),
		URL:       a.URL.String(),
	}
}

func convertReviewComment(c gqlReviewComment) ReviewComment {
	comment := ReviewComment{
		ID:         string(c.ID.(string)),
		DatabaseID: c.DatabaseID,
		Body:       string(c.Body),
		CreatedAt:  c.CreatedAt.Time,
		UpdatedAt:  c.UpdatedAt.Time,
		Author:     convertActor(c.Author),
	}
	if c.ReplyTo.DatabaseID != 0 {
		rid := fmt.Sprintf("%d", c.ReplyTo.DatabaseID)
		comment.ReplyToID = &rid
	}
	return comment
}

func convertIssueComment(c gqlIssueComment) IssueComment {
	return IssueComment{
		ID:         string(c.ID.(string)),
		DatabaseID: c.DatabaseID,
		Body:       string(c.Body),
		CreatedAt:  c.CreatedAt.Time,
		UpdatedAt:  c.UpdatedAt.Time,
		Author:     convertActor(c.Author),
	}
}

func convertReview(r gqlReview) Review {
	review := Review{
		ID:         string(r.ID.(string)),
		DatabaseID: r.DatabaseID,
		State:      ReviewState(r.State),
		Body:       string(r.Body),
		CreatedAt:  r.CreatedAt.Time,
		Author:     convertActor(r.Author),
	}
	if r.SubmittedAt != nil {
		t := r.SubmittedAt.Time
		review.SubmittedAt = &t
	}
	return review
}
