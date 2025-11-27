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

	// Find new comments to send
	var newThreads []newThreadInfo
	var replies []replyInfo

	for _, thread := range pr.ReviewThreads {
		// Check if this is a new thread (no ID)
		if thread.ID == "" {
			if len(thread.Comments) == 0 {
				continue
			}
			c := thread.Comments[0]
			if !c.IsNew {
				continue
			}
			newThreads = append(newThreads, newThreadInfo{
				path:    thread.Path,
				line:    thread.Line,
				side:    thread.DiffSide,
				subject: thread.SubjectType,
				body:    c.Body,
			})
		} else {
			// Existing thread - look for new replies
			for _, c := range thread.Comments {
				if !c.IsNew {
					continue
				}
				// Reply to first comment in thread
				if len(thread.Comments) == 0 || thread.Comments[0].ID == "" {
					return fmt.Errorf("cannot find comment to reply to in thread %s:%d", thread.Path, thread.Line)
				}
				replies = append(replies, replyInfo{
					threadPath:    thread.Path,
					threadLine:    thread.Line,
					body:          c.Body,
					replyToNodeID: thread.Comments[0].ID,
				})
			}
		}
	}

	// Check for new issue comments
	var newIssueComments []string
	for _, c := range pr.IssueComments {
		if c.IsNew {
			newIssueComments = append(newIssueComments, c.Body)
		}
	}

	if len(newThreads) == 0 && len(replies) == 0 && len(newIssueComments) == 0 {
		fmt.Println("No new comments to send.")
		return nil
	}

	fmt.Printf("Found %d new thread(s), %d reply/replies, %d issue comment(s) to send.\n",
		len(newThreads), len(replies), len(newIssueComments))

	if flagSendDryRun {
		fmt.Println("\n=== DRY RUN ===")
		for _, t := range newThreads {
			fmt.Printf("\nNew thread on %s:%d (%s):\n  %s\n", t.path, t.line, t.side, t.body)
		}
		for _, r := range replies {
			fmt.Printf("\nReply in thread %s:%d:\n  %s\n", r.threadPath, r.threadLine, r.body)
		}
		for _, c := range newIssueComments {
			fmt.Printf("\nNew issue comment:\n  %s\n", c)
		}
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

	// Get or create pending review
	fmt.Print("Getting/creating pending review... ")
	reviewID, err := client.getOrCreatePendingReview(ctx, pr.ID, pr.HeadRefOID)
	if err != nil {
		return fmt.Errorf("getting/creating review: %w", err)
	}
	fmt.Println("done")

	// Add new threads
	for _, t := range newThreads {
		fmt.Printf("Adding thread on %s:%d... ", t.path, t.line)
		_, err := client.addReviewThread(ctx, pr.ID, reviewID, t.path, t.line, t.side, t.subject, t.body)
		if err != nil {
			return fmt.Errorf("adding thread: %w", err)
		}
		fmt.Println("done")
	}

	// Add replies
	for _, r := range replies {
		fmt.Printf("Adding reply in thread %s:%d... ", r.threadPath, r.threadLine)
		_, err := client.addReviewComment(ctx, reviewID, r.replyToNodeID, r.body)
		if err != nil {
			return fmt.Errorf("adding reply: %w", err)
		}
		fmt.Println("done")
	}

	// Submit the review
	reviewEvent := "COMMENT"
	if flagSendApprove {
		reviewEvent = "APPROVE"
	} else if flagSendRequestChanges {
		reviewEvent = "REQUEST_CHANGES"
	}
	fmt.Printf("Submitting review (%s)... ", reviewEvent)
	if err := client.submitReview(ctx, reviewID, reviewEvent); err != nil {
		return fmt.Errorf("submitting review: %w", err)
	}
	fmt.Println("done")

	// TODO: Handle issue comments (different API)

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

type newThreadInfo struct {
	path    string
	line    int
	side    DiffSide
	subject SubjectType
	body    string
}

type replyInfo struct {
	threadPath    string
	threadLine    int
	body          string
	replyToNodeID string
}
