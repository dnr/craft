package main

import (
	"encoding/json"
	"fmt"
	"os"

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
	flagDebugSendPending              bool
	flagDebugSendDiscardPendingReview bool
)

func init() {
	debugSendCmd.Flags().StringVar(&flagDebugSendInput, "input", "", "Input JSON file with new comments")
	debugSendCmd.Flags().StringVar(&flagDebugSendOwner, "owner", "", "Repository owner")
	debugSendCmd.Flags().StringVar(&flagDebugSendRepo, "repo", "", "Repository name")
	debugSendCmd.Flags().BoolVar(&flagDebugSendDryRun, "dry-run", false, "Print what would be sent without sending")
	debugSendCmd.Flags().BoolVar(&flagDebugSendApprove, "approve", false, "Submit review as approval")
	debugSendCmd.Flags().BoolVar(&flagDebugSendRequestChanges, "request-changes", false, "Submit review requesting changes")
	debugSendCmd.Flags().BoolVar(&flagDebugSendPending, "pending", false, "Leave review in pending state (don't submit)")
	debugSendCmd.Flags().BoolVar(&flagDebugSendDiscardPendingReview, "discard-pending-review", false, "Discard existing pending review if one exists")
	debugSendCmd.MarkFlagsMutuallyExclusive("approve", "request-changes", "pending")

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
	} else if flagDebugSendPending {
		review.ReviewEvent = "PENDING"
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

	if review.ReviewEvent == "PENDING" {
		fmt.Println("\nReview left in pending state")
	} else {
		fmt.Println("\nAll comments sent successfully!")
	}
	return nil
}
