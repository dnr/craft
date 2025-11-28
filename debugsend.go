package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/shurcooL/githubv4"
	"github.com/spf13/cobra"
)

var debugSendCmd = &cobra.Command{
	Use:   "debugsend",
	Short: "Send new comments from a PR JSON file to GitHub",
	Long: `Reads a PR JSON file and sends any comments marked as new (isNew: true) to GitHub.

This creates new review threads and replies as needed.

Example:
  craft debugsend --input pr-modified.json --owner myorg --repo myrepo`,
	RunE: runDebugSend,
}

var (
	flagDebugSendInput          string
	flagDebugSendOwner          string
	flagDebugSendRepo           string
	flagDebugSendDryRun         bool
	flagDebugSendApprove        bool
	flagDebugSendRequestChanges bool
)

func init() {
	debugSendCmd.Flags().StringVar(&flagDebugSendInput, "input", "", "Input JSON file with new comments")
	debugSendCmd.Flags().StringVar(&flagDebugSendOwner, "owner", "", "Repository owner")
	debugSendCmd.Flags().StringVar(&flagDebugSendRepo, "repo", "", "Repository name")
	debugSendCmd.Flags().BoolVar(&flagDebugSendDryRun, "dry-run", false, "Print what would be sent without sending")
	debugSendCmd.Flags().BoolVar(&flagDebugSendApprove, "approve", false, "Submit review as approval")
	debugSendCmd.Flags().BoolVar(&flagDebugSendRequestChanges, "request-changes", false, "Submit review requesting changes")
	debugSendCmd.MarkFlagsMutuallyExclusive("approve", "request-changes")

	debugSendCmd.MarkFlagRequired("input")
	debugSendCmd.MarkFlagRequired("owner")
	debugSendCmd.MarkFlagRequired("repo")
}

func runDebugSend(cmd *cobra.Command, args []string) error {
	// Load input JSON
	data, err := os.ReadFile(flagDebugSendInput)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	var pr PullRequest
	if err := json.Unmarshal(data, &pr); err != nil {
		return fmt.Errorf("parsing input JSON: %w", err)
	}

	// Collect new comments using shared code
	review, err := CollectNewComments(&pr)
	if err != nil {
		return err
	}

	if review.IsEmpty() {
		fmt.Println("No new comments to send.")
		return nil
	}

	// Set review event
	if flagDebugSendApprove {
		review.ReviewEvent = "APPROVE"
	} else if flagDebugSendRequestChanges {
		review.ReviewEvent = "REQUEST_CHANGES"
	}

	fmt.Printf("Found %s\n", review.Summary())

	if flagDebugSendDryRun {
		review.PrintDryRun()
		return nil
	}

	// Get GitHub token and create client
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("getting GitHub token: %w", err)
	}
	client := NewGitHubClient(token)

	// Send the review using shared code
	if err := review.Send(cmd.Context(), client, pr.ID, pr.HeadRefOID); err != nil {
		return err
	}

	fmt.Println("\nAll comments sent successfully!")
	return nil
}

// addReviewThread adds a new thread to a pending review.
func (c *GitHubClient) addReviewThread(ctx context.Context, prNodeID string, reviewID githubv4.ID, path string, line int, side DiffSide, subject SubjectType, body string) (string, error) {
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
		PullRequestID:       &prID,
		PullRequestReviewID: &reviewID,
		Path:                githubv4.String(path),
		Body:                githubv4.String(body),
		Line:                &lineVal,
		Side:                &sideVal,
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

	if mutation.AddPullRequestReviewThread.Thread.ID == nil {
		return "", fmt.Errorf("addPullRequestReviewThread returned nil thread ID")
	}

	return string(mutation.AddPullRequestReviewThread.Thread.ID.(string)), nil
}

// addReviewComment adds a reply comment to a pending review.
func (c *GitHubClient) addReviewComment(ctx context.Context, reviewID githubv4.ID, replyToNodeID, body string) (string, error) {
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

	return string(mutation.AddPullRequestReviewComment.Comment.ID.(string)), nil
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

// submitReview submits a pending review with the given event type (COMMENT, APPROVE, REQUEST_CHANGES).
// The body is optional and becomes the top-level review comment.
func (c *GitHubClient) submitReview(ctx context.Context, reviewID githubv4.ID, eventType, body string) error {
	var mutation struct {
		SubmitPullRequestReview struct {
			PullRequestReview struct {
				ID githubv4.ID
			}
		} `graphql:"submitPullRequestReview(input: $input)"`
	}

	event := githubv4.PullRequestReviewEvent(eventType)

	input := githubv4.SubmitPullRequestReviewInput{
		PullRequestReviewID: &reviewID,
		Event:               event,
	}

	if body != "" {
		bodyVal := githubv4.String(body)
		input.Body = &bodyVal
	}

	vars := map[string]interface{}{
		"input": input,
	}

	return c.client.Mutate(ctx, &mutation, input, vars)
}
