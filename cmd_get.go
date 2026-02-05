package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get [pr-number]",
	Short: "Fetch a PR and set up for review",
	Long: `Fetches a pull request from GitHub, creates a local branch, and
serializes the PR review state into the source files.

If no PR number is given and you're already on a pr-N branch, it refreshes
that PR.

Examples:
  craft get 123        # Fetch PR #123
  craft get            # Refresh current PR`,
	RunE: runGet,
	Args: cobra.MaximumNArgs(1),
}

var (
	flagGetRemote string
	flagGetForce  bool
)

func init() {
	getCmd.Flags().StringVar(&flagGetRemote, "remote", "", "Git remote name (default: from config or 'origin')")
	getCmd.Flags().BoolVar(&flagGetForce, "force", false, "Force refresh even with uncommitted changes")
}

func runGet(cmd *cobra.Command, args []string) error {
	// Detect VCS
	vcs, err := DetectVCS(".")
	if err != nil {
		return err
	}
	fmt.Printf("Using %s repository at %s\n", vcs.Name(), vcs.Root())

	// Determine remote
	remote := flagGetRemote
	if remote == "" {
		remote, _ = vcs.GetConfigValue("craft.remoteName")
		if remote == "" {
			remote = "origin"
		}
	}

	// Get GitHub owner/repo from remote
	remoteURL, err := vcs.GetRemoteURL(remote)
	if err != nil {
		return fmt.Errorf("getting remote URL: %w", err)
	}
	owner, repo, err := ParseGitHubRemote(remoteURL)
	if err != nil {
		return err
	}
	fmt.Printf("GitHub repo: %s/%s\n", owner, repo)

	// Determine PR number
	var prNumber int
	if len(args) == 1 {
		prNumber, err = strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[0])
		}
	} else {
		// Try to get from current branch name
		branch, err := vcs.GetCurrentBranch()
		if err != nil {
			return fmt.Errorf("getting current branch: %w", err)
		}
		if strings.HasPrefix(branch, "pr-") {
			prNumber, err = strconv.Atoi(strings.TrimPrefix(branch, "pr-"))
			if err != nil {
				return fmt.Errorf("current branch %s is not a valid PR branch", branch)
			}
		} else {
			return fmt.Errorf("no PR number given and not on a pr-N branch")
		}
	}
	fmt.Printf("PR number: %d\n", prNumber)

	// Check for uncommitted changes
	if !flagGetForce {
		hasChanges, err := vcs.HasUncommittedChanges()
		if err != nil {
			return fmt.Errorf("checking for uncommitted changes: %w", err)
		}
		if hasChanges {
			return fmt.Errorf("uncommitted changes detected; use --force to discard or commit/send first")
		}
	}

	// Get GitHub token
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("getting GitHub token: %w", err)
	}
	client := NewGitHubClient(token)

	// Fetch PR data from GitHub API
	fmt.Print("Fetching PR data from GitHub... ")
	pr, err := client.FetchPullRequest(cmd.Context(), owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	fmt.Println("done")
	fmt.Printf("PR: %s\n", pr.Title)
	fmt.Printf("Head: %s (%s)\n", pr.HeadRefName, pr.HeadRefOID[:12])

	// Fetch the PR branch from remote
	fmt.Print("Fetching PR branch... ")
	if err := vcs.FetchPRBranch(remote, prNumber); err != nil {
		return fmt.Errorf("fetching PR branch: %w", err)
	}
	fmt.Println("done")

	// Create/switch to local branch
	fmt.Print("Switching to local branch... ")
	if err := vcs.CreateAndSwitchBranch(prNumber, pr.HeadRefOID); err != nil {
		return fmt.Errorf("creating branch: %w", err)
	}
	fmt.Println("done")

	// Serialize PR state to files
	fmt.Print("Serializing PR state... ")
	opts := SerializeOptions{FS: DirFS(vcs.Root()), VCS: vcs}
	if err := Serialize(pr, opts); err != nil {
		return fmt.Errorf("serializing: %w", err)
	}
	fmt.Println("done")

	// Commit the changes
	fmt.Print("Committing... ")
	commitMsg := fmt.Sprintf("craft: PR #%d state\n\n%s", prNumber, pr.Title)
	if err := vcs.Commit(commitMsg); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	fmt.Println("done")

	// Summary
	fmt.Printf("\nReady for review on branch pr-%d\n", prNumber)
	fmt.Printf("  %d review threads\n", len(pr.ReviewThreads))
	fmt.Printf("  %d issue comments\n", len(pr.IssueComments))

	return nil
}
