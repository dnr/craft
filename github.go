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

// FetchPullRequest fetches all PR data including review threads, comments, and reviews.
func (c *GitHubClient) FetchPullRequest(ctx context.Context, owner, repo string, number int) (*PullRequest, error) {
	// Query for basic PR info and review threads
	var prQuery struct {
		Repository struct {
			PullRequest struct {
				ID          githubv4.ID
				Number      githubv4.Int
				Title       githubv4.String
				Body        githubv4.String
				State       githubv4.String
				IsDraft     githubv4.Boolean
				BaseRefName githubv4.String
				HeadRefName githubv4.String
				BaseRefOid  githubv4.GitObjectID
				HeadRefOid  githubv4.GitObjectID
				Author      struct {
					Login     githubv4.String
					AvatarURL githubv4.URI `graphql:"avatarUrl"`
					URL       githubv4.URI `graphql:"url"`
				}
				ReviewThreads struct {
					PageInfo struct {
						HasNextPage githubv4.Boolean
						EndCursor   githubv4.String
					}
					Nodes []struct {
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
							Nodes []struct {
								ID         githubv4.ID
								DatabaseID int64
								Body       githubv4.String
								CreatedAt  githubv4.DateTime
								UpdatedAt  githubv4.DateTime
								Author     struct {
									Login     githubv4.String
									AvatarURL githubv4.URI `graphql:"avatarUrl"`
									URL       githubv4.URI `graphql:"url"`
								}
								ReplyTo struct {
									DatabaseID int64
								}
							}
						} `graphql:"comments(first: 100)"`
					}
				} `graphql:"reviewThreads(first: 100)"`
				Comments struct {
					PageInfo struct {
						HasNextPage githubv4.Boolean
						EndCursor   githubv4.String
					}
					Nodes []struct {
						ID         githubv4.ID
						DatabaseID int64
						Body       githubv4.String
						CreatedAt  githubv4.DateTime
						UpdatedAt  githubv4.DateTime
						Author     struct {
							Login     githubv4.String
							AvatarURL githubv4.URI `graphql:"avatarUrl"`
							URL       githubv4.URI `graphql:"url"`
						}
					}
				} `graphql:"comments(first: 100)"`
				Reviews struct {
					PageInfo struct {
						HasNextPage githubv4.Boolean
						EndCursor   githubv4.String
					}
					Nodes []struct {
						ID          githubv4.ID
						DatabaseID  int64
						State       githubv4.String
						Body        githubv4.String
						SubmittedAt *githubv4.DateTime
						CreatedAt   githubv4.DateTime
						Author      struct {
							Login     githubv4.String
							AvatarURL githubv4.URI `graphql:"avatarUrl"`
							URL       githubv4.URI `graphql:"url"`
						}
					}
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
		Author: Actor{
			Login:     string(ghPR.Author.Login),
			AvatarURL: ghPR.Author.AvatarURL.String(),
			URL:       ghPR.Author.URL.String(),
		},
	}

	// Convert review threads
	for _, t := range ghPR.ReviewThreads.Nodes {
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

		for _, c := range t.Comments.Nodes {
			comment := ReviewComment{
				ID:         string(c.ID.(string)),
				DatabaseID: c.DatabaseID,
				Body:       string(c.Body),
				CreatedAt:  c.CreatedAt.Time,
				UpdatedAt:  c.UpdatedAt.Time,
				Author: Actor{
					Login:     string(c.Author.Login),
					AvatarURL: c.Author.AvatarURL.String(),
					URL:       c.Author.URL.String(),
				},
			}
			if c.ReplyTo.DatabaseID != 0 {
				rid := fmt.Sprintf("%d", c.ReplyTo.DatabaseID)
				comment.ReplyToID = &rid
			}
			thread.Comments = append(thread.Comments, comment)
		}
		pr.ReviewThreads = append(pr.ReviewThreads, thread)
	}

	// Convert issue comments
	for _, c := range ghPR.Comments.Nodes {
		pr.IssueComments = append(pr.IssueComments, IssueComment{
			ID:         string(c.ID.(string)),
			DatabaseID: c.DatabaseID,
			Body:       string(c.Body),
			CreatedAt:  c.CreatedAt.Time,
			UpdatedAt:  c.UpdatedAt.Time,
			Author: Actor{
				Login:     string(c.Author.Login),
				AvatarURL: c.Author.AvatarURL.String(),
				URL:       c.Author.URL.String(),
			},
		})
	}

	// Convert reviews
	for _, r := range ghPR.Reviews.Nodes {
		review := Review{
			ID:         string(r.ID.(string)),
			DatabaseID: r.DatabaseID,
			State:      ReviewState(r.State),
			Body:       string(r.Body),
			CreatedAt:  r.CreatedAt.Time,
			Author: Actor{
				Login:     string(r.Author.Login),
				AvatarURL: r.Author.AvatarURL.String(),
				URL:       r.Author.URL.String(),
			},
		}
		if r.SubmittedAt != nil {
			t := r.SubmittedAt.Time
			review.SubmittedAt = &t
		}
		pr.Reviews = append(pr.Reviews, review)
	}

	return pr, nil
}
