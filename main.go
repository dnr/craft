package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// Comment formatting constants
	CraftMarker       = "❯" // U+276F
	RuleChar          = "─" // U+2500
	TimeFormat        = "2006-01-02 15:04"
	BranchPrefix      = "pr-"
	PRCommentsFile    = "PR-COMMENTS.txt"
	NewCommentPrefix  = "+: "
	LeadingDashes     = 5
	MaxLineLength     = 100
	CommitMsgTemplate = "craft: embed PR #%d review comments"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "get":
		handleGet(os.Args[2:])
	case "send":
		handleSend(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("craft - GitHub code review tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  craft get [<pr#>]    Get PR for review")
	fmt.Println("  craft send [--go]    Send review comments")
}

func handleGet(args []string) {
	owner, repo, err := getRepoInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var prNumber int
	if len(args) > 0 {
		prNumber, err = strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid PR number '%s'\n", args[0])
			os.Exit(1)
		}
	} else {
		// Try to extract PR number from current branch name
		currentBranch, err := getCurrentBranch()
		if err == nil && strings.HasPrefix(currentBranch, BranchPrefix) {
			prNumberStr := strings.TrimPrefix(currentBranch, BranchPrefix)
			if extractedPR, err := strconv.Atoi(prNumberStr); err == nil {
				prNumber = extractedPR
				fmt.Printf("Using PR number %d from current branch '%s'\n", prNumber, currentBranch)
			} else {
				fmt.Fprintf(os.Stderr, "Error: PR number required\n")
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: PR number required\n")
			os.Exit(1)
		}
	}

	client := createGitHubClient()
	ctx := context.Background()

	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PR: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found PR #%d: %s\n", prNumber, pr.GetTitle())
	fmt.Printf("Branch: %s\n", pr.GetHead().GetRef())
	fmt.Printf("Base: %s\n", pr.GetBase().GetRef())

	// Check for uncommitted changes
	if hasUncommittedChanges() {
		fmt.Fprintf(os.Stderr, "Error: You have uncommitted changes. Please commit or stash them first.\n")
		os.Exit(1)
	}

	// Create and switch to PR branch
	branchName := fmt.Sprintf("%s%d", BranchPrefix, prNumber)
	headRef := pr.GetHead().GetRef()
	headSHA := pr.GetHead().GetSHA()

	err = checkoutPRBranch(branchName, headRef, headSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking out branch: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Switched to branch '%s'\n", branchName)

	// Fetch and embed PR comments
	fmt.Println("Fetching PR comments...")
	err = embedPRComments(client, ctx, owner, repo, prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error embedding comments: %v\n", err)
		os.Exit(1)
	}

	// Auto-commit the embedded comments to avoid uncommitted changes
	err = commitEmbeddedComments(prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to commit embedded comments: %v\n", err)
	}

	fmt.Println("PR ready for review!")
}

func handleSend(args []string) {
	fmt.Println("send command - not implemented yet")
	for _, arg := range args {
		if arg == "--go" {
			fmt.Println("--go flag detected")
		}
	}
}
