package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send review comments to GitHub",
	Long: `Reads review comments from source files and sends new ones to GitHub.

Must be run from a pr-N branch created by 'craft get'.

Examples:
  craft send                    # Send as comment
  craft send --approve          # Send and approve
  craft send --request-changes  # Send and request changes
  craft send --dry-run          # Show what would be sent`,
	RunE: runSend,
}

var (
	flagSendDryRun               bool
	flagSendApprove              bool
	flagSendRequestChanges       bool
	flagSendDiscardPendingReview bool
	flagSendPending              bool
	flagSendReplyOnly            bool
)

func init() {
	sendCmd.Flags().BoolVar(&flagSendDryRun, "dry-run", false, "Print what would be sent without sending")
	sendCmd.Flags().BoolVar(&flagSendApprove, "approve", false, "Submit review as approval")
	sendCmd.Flags().BoolVar(&flagSendRequestChanges, "request-changes", false, "Submit review requesting changes")
	sendCmd.Flags().BoolVar(&flagSendDiscardPendingReview, "discard-pending-review", false, "Discard existing pending review if one exists (required when adding new threads)")
	sendCmd.Flags().BoolVar(&flagSendPending, "pending", false, "Leave review in pending state (don't submit)")
	sendCmd.Flags().BoolVar(&flagSendReplyOnly, "reply-only", false, "Send only replies to existing threads (skip code change check, skip re-serialize)")
	sendCmd.MarkFlagsMutuallyExclusive("approve", "request-changes", "pending")
}

func runSend(cmd *cobra.Command, args []string) error {
	// Detect VCS
	vcs, err := DetectVCS(".")
	if err != nil {
		return err
	}

	// Get current branch to determine PR number
	prNumber, err := prNumberFromBranch(vcs)
	if err != nil {
		return err
	}
	fmt.Printf("PR #%d\n", prNumber)

	// Deserialize PR state from files
	fmt.Print("Reading PR state from files... ")
	opts := SerializeOptions{FS: DirFS(vcs.Root()), VCS: vcs}
	pr, err := Deserialize(opts)
	if err != nil {
		return fmt.Errorf("deserializing: %w", err)
	}
	fmt.Println("done")

	// Require that craft get was run first
	if pr.ID == "" {
		return fmt.Errorf("PR-STATE.txt missing PR ID; run 'craft get' first")
	}

	// Check for non-craft code changes (skip in reply-only mode)
	if pr.HeadRefOID != "" && !flagSendReplyOnly {
		fmt.Print("Checking for code changes... ")
		if err := CheckForNonCraftChanges(vcs, pr.HeadRefOID); err != nil {
			fmt.Println("found!")
			return err
		}
		fmt.Println("ok")
	}

	// Collect new comments
	review, err := CollectNewComments(pr)
	if err != nil {
		return err
	}

	// In reply-only mode, error if there are new threads
	if flagSendReplyOnly && len(review.NewThreads) > 0 {
		return fmt.Errorf("--reply-only: found %d new thread(s); only replies to existing threads are allowed in this mode", len(review.NewThreads))
	}

	// Set review event
	if flagSendApprove {
		review.ReviewEvent = "APPROVE"
	} else if flagSendRequestChanges {
		review.ReviewEvent = "REQUEST_CHANGES"
	} else if flagSendPending {
		review.ReviewEvent = "PENDING"
	}

	if review.IsEmpty() && review.ReviewEvent != "APPROVE" {
		fmt.Println("No new comments to send.")
		return nil
	}

	fmt.Printf("Found %s\n", review.Summary())

	if flagSendDryRun {
		review.PrintDryRun()
		return nil
	}

	// Get GitHub token and remote info
	remote := resolveRemote(vcs, "")
	client, owner, repo, err := getGitHubClientAndRepo(vcs, remote)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	// Check if PR head has changed
	fmt.Print("Checking PR status... ")
	currentHead, err := client.FetchPRHead(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("checking PR head: %w", err)
	}
	if currentHead != pr.HeadRefOID {
		fmt.Println("changed!")
		fmt.Printf("\nPR has been updated (local: %s, remote: %s)\n", pr.HeadRefOID[:12], currentHead[:12])
		fmt.Println("\nTo update your local state while preserving your comments:")
		fmt.Println("  1. Commit your changes:  git add -A && git commit -m 'my comments'")
		fmt.Println("  2. Fetch new PR head:    git fetch origin refs/pull/" + fmt.Sprint(prNumber) + "/head")
		fmt.Println("  3. Merge:                git merge FETCH_HEAD")
		fmt.Println("  4. Resolve any conflicts, then run 'craft send' again")
		return fmt.Errorf("PR head has changed; merge required")
	}
	fmt.Println("ok")

	// Send the review
	if err := review.Send(ctx, client, pr.ID, pr.HeadRefOID, flagSendDiscardPendingReview); err != nil {
		return err
	}

	if flagSendReplyOnly {
		// In reply-only mode, skip re-fetch/re-serialize to preserve code edits.
		// The user is expected to run 'craft clear' next.
		fmt.Println("\nReplies sent successfully! (reply-only mode, files unchanged)")
		fmt.Println("Run 'craft clear' to remove craft comments from source files.")
		return nil
	}

	// Re-fetch PR to get updated state with our new comments
	fmt.Print("Fetching updated PR state... ")
	updatedPR, err := client.FetchPullRequest(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching updated PR: %w", err)
	}
	fmt.Println("done")

	// Re-serialize (comments are no longer "new")
	fmt.Print("Updating local files... ")
	if err := Serialize(updatedPR, opts); err != nil {
		return fmt.Errorf("serializing: %w", err)
	}
	fmt.Println("done")

	// Commit the changes
	fmt.Print("Committing... ")
	commitMsg := fmt.Sprintf("craft: sent review on PR #%d", prNumber)
	if err := vcs.Commit(commitMsg); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	fmt.Println("done")

	if review.ReviewEvent == "PENDING" {
		fmt.Println("\nReview left in pending state")
	} else {
		fmt.Println("\nReview sent successfully!")
	}
	return nil
}
