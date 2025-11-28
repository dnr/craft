package main

import (
	"fmt"
	"strconv"
	"strings"

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
	flagSendDryRun         bool
	flagSendApprove        bool
	flagSendRequestChanges bool
)

func init() {
	sendCmd.Flags().BoolVar(&flagSendDryRun, "dry-run", false, "Print what would be sent without sending")
	sendCmd.Flags().BoolVar(&flagSendApprove, "approve", false, "Submit review as approval")
	sendCmd.Flags().BoolVar(&flagSendRequestChanges, "request-changes", false, "Submit review requesting changes")
	sendCmd.MarkFlagsMutuallyExclusive("approve", "request-changes")
}

func runSend(cmd *cobra.Command, args []string) error {
	// Detect VCS
	vcs, err := DetectVCS(".")
	if err != nil {
		return err
	}

	// Get current branch to determine PR number
	branch, err := vcs.GetCurrentBranch()
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	if !strings.HasPrefix(branch, "pr-") {
		return fmt.Errorf("not on a pr-N branch (current: %s)", branch)
	}
	prNumber, err := strconv.Atoi(strings.TrimPrefix(branch, "pr-"))
	if err != nil {
		return fmt.Errorf("invalid branch name: %s", branch)
	}
	fmt.Printf("PR #%d\n", prNumber)

	// Deserialize PR state from files
	fmt.Print("Reading PR state from files... ")
	opts := SerializeOptions{FS: DirFS(vcs.Root())}
	pr, err := Deserialize(opts)
	if err != nil {
		return fmt.Errorf("deserializing: %w", err)
	}
	fmt.Println("done")

	// Require that craft get was run first
	if pr.ID == "" {
		return fmt.Errorf("PR-STATE.txt missing PR ID; run 'craft get' first")
	}

	// Collect new comments
	review, err := CollectNewComments(pr)
	if err != nil {
		return err
	}

	if review.IsEmpty() {
		fmt.Println("No new comments to send.")
		return nil
	}

	// Set review event
	if flagSendApprove {
		review.ReviewEvent = "APPROVE"
	} else if flagSendRequestChanges {
		review.ReviewEvent = "REQUEST_CHANGES"
	}

	fmt.Printf("Found %s\n", review.Summary())

	if flagSendDryRun {
		review.PrintDryRun()
		return nil
	}

	// Get GitHub token and remote info
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("getting GitHub token: %w", err)
	}
	client := NewGitHubClient(token)

	// Get owner/repo from remote
	remote, _ := vcs.GetConfigValue("craft.remoteName")
	if remote == "" {
		remote = "origin"
	}
	remoteURL, err := vcs.GetRemoteURL(remote)
	if err != nil {
		return fmt.Errorf("getting remote URL: %w", err)
	}
	owner, repo, err := ParseGitHubRemote(remoteURL)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	// Send the review
	if err := review.Send(ctx, client, pr.ID, pr.HeadRefOID); err != nil {
		return err
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

	fmt.Println("\nReview sent successfully!")
	return nil
}
