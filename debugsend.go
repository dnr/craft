package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/shurcooL/githubv4"
	"github.com/spf13/cobra"
)

var debugSendCmd = &cobra.Command{
	Use:   "debugsend",
	Short: "Send new comments from a PR JSON file to GitHub",
	Long: `Reads a PR JSON file and sends any comments marked as new (isNew: true) to GitHub.

This creates new review threads and replies as needed. Comments are sent as
single comments (immediately submitted, not as a pending review).

Example:
  craft debugsend --input pr-modified.json --owner myorg --repo myrepo`,
	RunE: runDebugSend,
}

var (
	flagSendInput string
	flagSendOwner string
	flagSendRepo  string
	flagDryRun    bool
)

func init() {
	debugSendCmd.Flags().StringVar(&flagSendInput, "input", "", "Input JSON file with new comments")
	debugSendCmd.Flags().StringVar(&flagSendOwner, "owner", "", "Repository owner")
	debugSendCmd.Flags().StringVar(&flagSendRepo, "repo", "", "Repository name")
	debugSendCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Print what would be sent without sending")

	debugSendCmd.MarkFlagRequired("input")
	debugSendCmd.MarkFlagRequired("owner")
	debugSendCmd.MarkFlagRequired("repo")
}

func runDebugSend(cmd *cobra.Command, args []string) error {
	// Load input JSON
	data, err := os.ReadFile(flagSendInput)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	var pr PullRequest
	if err := json.Unmarshal(data, &pr); err != nil {
		return fmt.Errorf("parsing input JSON: %w", err)
	}

	// Build lookup map: databaseId -> node ID for existing comments
	commentNodeIDs := make(map[int64]string)
	for _, thread := range pr.ReviewThreads {
		for _, c := range thread.Comments {
			if c.ID != "" && c.DatabaseID != 0 {
				commentNodeIDs[c.DatabaseID] = c.ID
			}
		}
	}

	// Find all new comments to send
	var newThreads []newThreadRequest
	var replies []replyRequest

	for _, thread := range pr.ReviewThreads {
		if thread.ID == "" {
			// New thread - the first comment should be new
			if len(thread.Comments) == 0 {
				continue
			}
			c := thread.Comments[0]
			if !c.IsNew {
				continue
			}
			newThreads = append(newThreads, newThreadRequest{
				path:    thread.Path,
				line:    thread.Line,
				side:    thread.DiffSide,
				body:    c.Body,
				subject: thread.SubjectType,
			})
		} else {
			// Existing thread - look for new replies
			for _, c := range thread.Comments {
				if !c.IsNew {
					continue
				}
				// Find the comment to reply to
				var replyToNodeID string
				if c.ReplyToID != nil {
					dbID, err := strconv.ParseInt(*c.ReplyToID, 10, 64)
					if err == nil {
						replyToNodeID = commentNodeIDs[dbID]
					}
				}
				if replyToNodeID == "" {
					// Default to first comment in thread
					if len(thread.Comments) > 0 && thread.Comments[0].ID != "" {
						replyToNodeID = thread.Comments[0].ID
					}
				}
				if replyToNodeID == "" {
					return fmt.Errorf("cannot find comment to reply to in thread %s:%d", thread.Path, thread.Line)
				}
				replies = append(replies, replyRequest{
					threadPath:    thread.Path,
					threadLine:    thread.Line,
					body:          c.Body,
					replyToNodeID: replyToNodeID,
				})
			}
		}
	}

	if len(newThreads) == 0 && len(replies) == 0 {
		fmt.Println("No new comments to send.")
		return nil
	}

	fmt.Printf("Found %d new thread(s) and %d reply/replies to send.\n", len(newThreads), len(replies))

	if flagDryRun {
		fmt.Println("\n=== DRY RUN - would send: ===")
		for _, t := range newThreads {
			fmt.Printf("\nNew thread on %s:%d (%s side):\n  %s\n", t.path, t.line, t.side, t.body)
		}
		for _, r := range replies {
			fmt.Printf("\nReply in thread %s:%d (to %s):\n  %s\n", r.threadPath, r.threadLine, r.replyToNodeID, r.body)
		}
		return nil
	}

	// Get GitHub token and create client
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("getting GitHub token: %w", err)
	}
	client := NewGitHubClient(token)

	ctx := cmd.Context()

	// Send new threads
	for _, t := range newThreads {
		fmt.Printf("Creating thread on %s:%d... ", t.path, t.line)
		threadID, err := client.CreateReviewThread(ctx, pr.ID, t.path, t.line, t.side, t.subject, t.body)
		if err != nil {
			return fmt.Errorf("creating thread: %w", err)
		}
		fmt.Printf("done (id: %s)\n", threadID)
	}

	// Send replies
	for _, r := range replies {
		fmt.Printf("Creating reply in thread %s:%d... ", r.threadPath, r.threadLine)
		commentID, err := client.CreateReviewReply(ctx, pr.ID, pr.HeadRefOID, r.replyToNodeID, r.body)
		if err != nil {
			return fmt.Errorf("creating reply: %w", err)
		}
		fmt.Printf("done (id: %s)\n", commentID)
	}

	fmt.Println("\nAll comments sent successfully!")
	return nil
}

type newThreadRequest struct {
	path    string
	line    int
	side    DiffSide
	subject SubjectType
	body    string
}

type replyRequest struct {
	threadPath    string
	threadLine    int
	body          string
	replyToNodeID string
}

// CreateReviewThread creates a new review thread with a single comment (immediately submitted).
func (c *GitHubClient) CreateReviewThread(ctx context.Context, prNodeID, path string, line int, side DiffSide, subject SubjectType, body string) (string, error) {
	var mutation struct {
		AddPullRequestReviewThread struct {
			Thread struct {
				ID githubv4.ID
			}
		} `graphql:"addPullRequestReviewThread(input: $input)"`
	}

	lineVal := githubv4.Int(line)
	sideVal := githubv4.DiffSide(side)
	prID := githubv4.ID(prNodeID)

	input := githubv4.AddPullRequestReviewThreadInput{
		PullRequestID: &prID,
		Path:          githubv4.String(path),
		Body:          githubv4.String(body),
		Line:          &lineVal,
		Side:          &sideVal,
	}

	if subject == SubjectTypeLine {
		st := githubv4.PullRequestReviewThreadSubjectType("LINE")
		input.SubjectType = &st
	} else if subject == SubjectTypeFile {
		st := githubv4.PullRequestReviewThreadSubjectType("FILE")
		input.SubjectType = &st
	}

	vars := map[string]interface{}{
		"input": input,
	}

	if err := c.client.Mutate(ctx, &mutation, input, vars); err != nil {
		return "", fmt.Errorf("addPullRequestReviewThread mutation failed: %w", err)
	}

	return string(mutation.AddPullRequestReviewThread.Thread.ID.(string)), nil
}

// CreateReviewReply creates a reply to an existing comment.
// This requires a pending review, either existing or newly created.
func (c *GitHubClient) CreateReviewReply(ctx context.Context, prNodeID, commitOID, replyToNodeID, body string) (string, error) {
	// Check for existing pending review, or create a new one
	reviewID, err := c.getOrCreatePendingReview(ctx, prNodeID, commitOID)
	if err != nil {
		return "", fmt.Errorf("getting/creating review: %w", err)
	}

	// Add the comment to the review
	var mutation struct {
		AddPullRequestReviewComment struct {
			Comment struct {
				ID githubv4.ID
			}
		} `graphql:"addPullRequestReviewComment(input: $input)"`
	}

	bodyVal := githubv4.String(body)
	replyToID := githubv4.ID(replyToNodeID)

	input := githubv4.AddPullRequestReviewCommentInput{
		PullRequestReviewID: &reviewID,
		Body:                &bodyVal,
		InReplyTo:           &replyToID,
	}

	vars := map[string]interface{}{
		"input": input,
	}

	if err := c.client.Mutate(ctx, &mutation, input, vars); err != nil {
		return "", fmt.Errorf("addPullRequestReviewComment mutation failed: %w", err)
	}

	commentID := string(mutation.AddPullRequestReviewComment.Comment.ID.(string))

	// Submit the review immediately (as COMMENT, not approval/rejection)
	if err := c.submitReview(ctx, reviewID); err != nil {
		return commentID, fmt.Errorf("submitting review: %w", err)
	}

	return commentID, nil
}

// getOrCreatePendingReview finds an existing pending review or creates a new one.
func (c *GitHubClient) getOrCreatePendingReview(ctx context.Context, prNodeID, commitOID string) (githubv4.ID, error) {
	// First, check for existing pending review
	var query struct {
		Node struct {
			PullRequest struct {
				Reviews struct {
					Nodes []struct {
						ID githubv4.ID
					}
				} `graphql:"reviews(first: 1, states: PENDING)"`
			} `graphql:"... on PullRequest"`
		} `graphql:"node(id: $id)"`
	}

	vars := map[string]interface{}{
		"id": githubv4.ID(prNodeID),
	}

	if err := c.client.Query(ctx, &query, vars); err != nil {
		return nil, fmt.Errorf("checking for pending review: %w", err)
	}

	if len(query.Node.PullRequest.Reviews.Nodes) > 0 {
		return query.Node.PullRequest.Reviews.Nodes[0].ID, nil
	}

	// No pending review, create one
	return c.startReview(ctx, prNodeID, commitOID)
}

// startReview creates a new pending review and returns its ID.
func (c *GitHubClient) startReview(ctx context.Context, prNodeID, commitOID string) (githubv4.ID, error) {
	var mutation struct {
		AddPullRequestReview struct {
			PullRequestReview struct {
				ID githubv4.ID
			}
		} `graphql:"addPullRequestReview(input: $input)"`
	}

	prID := githubv4.ID(prNodeID)
	commit := githubv4.GitObjectID(commitOID)

	input := githubv4.AddPullRequestReviewInput{
		PullRequestID: &prID,
		CommitOID:     &commit,
	}

	vars := map[string]interface{}{
		"input": input,
	}

	if err := c.client.Mutate(ctx, &mutation, input, vars); err != nil {
		return nil, err
	}

	return mutation.AddPullRequestReview.PullRequestReview.ID, nil
}

// submitReview submits a pending review as a COMMENT (not approval/rejection).
func (c *GitHubClient) submitReview(ctx context.Context, reviewID githubv4.ID) error {
	var mutation struct {
		SubmitPullRequestReview struct {
			PullRequestReview struct {
				ID githubv4.ID
			}
		} `graphql:"submitPullRequestReview(input: $input)"`
	}

	event := githubv4.PullRequestReviewEvent("COMMENT")

	input := githubv4.SubmitPullRequestReviewInput{
		PullRequestReviewID: &reviewID,
		Event:               event,
	}

	vars := map[string]interface{}{
		"input": input,
	}

	return c.client.Mutate(ctx, &mutation, input, vars)
}
