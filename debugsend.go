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
	flagDebugSendInput                string
	flagDebugSendOwner                string
	flagDebugSendRepo                 string
	flagDebugSendDryRun               bool
	flagDebugSendApprove              bool
	flagDebugSendRequestChanges       bool
	flagDebugSendDiscardPendingReview bool
)

func init() {
	debugSendCmd.Flags().StringVar(&flagDebugSendInput, "input", "", "Input JSON file with new comments")
	debugSendCmd.Flags().StringVar(&flagDebugSendOwner, "owner", "", "Repository owner")
	debugSendCmd.Flags().StringVar(&flagDebugSendRepo, "repo", "", "Repository name")
	debugSendCmd.Flags().BoolVar(&flagDebugSendDryRun, "dry-run", false, "Print what would be sent without sending")
	debugSendCmd.Flags().BoolVar(&flagDebugSendApprove, "approve", false, "Submit review as approval")
	debugSendCmd.Flags().BoolVar(&flagDebugSendRequestChanges, "request-changes", false, "Submit review requesting changes")
	debugSendCmd.Flags().BoolVar(&flagDebugSendDiscardPendingReview, "discard-pending-review", false, "Discard existing pending review if one exists")
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
	if err := review.Send(cmd.Context(), client, pr.ID, pr.HeadRefOID, flagDebugSendDiscardPendingReview); err != nil {
		return err
	}

	fmt.Println("\nAll comments sent successfully!")
	return nil
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

	if err := c.client.Mutate(ctx, &mutation, input, nil); err != nil {
		return "", fmt.Errorf("addPullRequestReviewComment mutation failed: %w", err)
	}

	return string(mutation.AddPullRequestReviewComment.Comment.ID.(string)), nil
}

// getPendingReview checks if there's an existing pending review.
// Returns the review ID (if any), whether one exists, and any error.
func (c *GitHubClient) getPendingReview(ctx context.Context, prNodeID string) (githubv4.ID, bool, error) {
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
		return nil, false, fmt.Errorf("checking for pending review: %w", err)
	}

	if len(query.Node.PullRequest.Reviews.Nodes) > 0 {
		return query.Node.PullRequest.Reviews.Nodes[0].ID, true, nil
	}

	return nil, false, nil
}

// deletePendingReview deletes a pending review.
func (c *GitHubClient) deletePendingReview(ctx context.Context, reviewID githubv4.ID) error {
	var mutation struct {
		DeletePullRequestReview struct {
			PullRequestReview struct {
				ID githubv4.ID
			}
		} `graphql:"deletePullRequestReview(input: $input)"`
	}

	input := githubv4.DeletePullRequestReviewInput{
		PullRequestReviewID: reviewID,
	}

	return c.client.Mutate(ctx, &mutation, input, nil)
}

// startReviewWithThreads creates a new pending review with threads and returns its ID.
// This works around a GitHub bug where adding threads to an existing review fails silently.
func (c *GitHubClient) startReviewWithThreads(ctx context.Context, prNodeID, commitOID string, threads []NewThreadInfo) (githubv4.ID, error) {
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

	// Add threads if provided
	if len(threads) > 0 {
		draftThreads := make([]*githubv4.DraftPullRequestReviewThread, len(threads))
		for i, t := range threads {
			line := githubv4.Int(t.Line)
			side := githubv4.DiffSide(t.Side)
			dt := &githubv4.DraftPullRequestReviewThread{
				Path: githubv4.String(t.Path),
				Line: line,
				Body: githubv4.String(t.Body),
				Side: &side,
			}
			if t.StartLine != nil {
				startLine := githubv4.Int(*t.StartLine)
				dt.StartLine = &startLine
			}
			draftThreads[i] = dt
		}
		input.Threads = &draftThreads
	}

	if err := c.client.Mutate(ctx, &mutation, input, nil); err != nil {
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

	return c.client.Mutate(ctx, &mutation, input, nil)
}
