package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/google/go-github/v74/github"
	"golang.org/x/oauth2"
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
		fmt.Fprintf(os.Stderr, "Error: PR number required\n")
		os.Exit(1)
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
	branchName := fmt.Sprintf("pr-%d", prNumber)
	headRef := pr.GetHead().GetRef()
	headSHA := pr.GetHead().GetSHA()
	
	err = checkoutPRBranch(branchName, headRef, headSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking out branch: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("Switched to branch '%s'\n", branchName)
}

func handleSend(args []string) {
	fmt.Println("send command - not implemented yet")
	for _, arg := range args {
		if arg == "--go" {
			fmt.Println("--go flag detected")
		}
	}
}

func createGitHubClient() *github.Client {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: GITHUB_TOKEN environment variable is required")
		fmt.Fprintln(os.Stderr, "Create a personal access token at: https://github.com/settings/tokens")
		fmt.Fprintln(os.Stderr, "Then export GITHUB_TOKEN=your_token")
		os.Exit(1)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	
	return github.NewClient(tc)
}

func getRepoInfo() (owner, repo string, err error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("not in a git repository or no origin remote")
	}
	
	remoteURL := strings.TrimSpace(string(output))
	
	// Parse GitHub URL patterns:
	// https://github.com/owner/repo.git
	// git@github.com:owner/repo.git
	var repoPath string
	if strings.HasPrefix(remoteURL, "https://github.com/") {
		repoPath = strings.TrimPrefix(remoteURL, "https://github.com/")
	} else if strings.HasPrefix(remoteURL, "git@github.com:") {
		repoPath = strings.TrimPrefix(remoteURL, "git@github.com:")
	} else {
		return "", "", fmt.Errorf("remote origin is not a GitHub repository")
	}
	
	// Remove .git suffix
	repoPath = strings.TrimSuffix(repoPath, ".git")
	
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GitHub repository format")
	}
	
	return parts[0], parts[1], nil
}

func hasUncommittedChanges() bool {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return true // Assume changes if we can't check
	}
	return len(strings.TrimSpace(string(output))) > 0
}

func checkoutPRBranch(branchName, headRef, headSHA string) error {
	// First, try to fetch the PR branch if it doesn't exist locally
	cmd := exec.Command("git", "fetch", "origin", headRef)
	cmd.Run() // Ignore errors, branch might already exist
	
	// Check if local branch already exists
	cmd = exec.Command("git", "rev-parse", "--verify", branchName)
	if cmd.Run() == nil {
		// Branch exists, switch to it and pull latest
		cmd = exec.Command("git", "checkout", branchName)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to checkout existing branch %s: %v", branchName, err)
		}
		
		// Pull latest changes
		cmd = exec.Command("git", "pull", "origin", headRef)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to pull latest changes: %v", err)
		}
	} else {
		// Branch doesn't exist, create it
		cmd = exec.Command("git", "checkout", "-b", branchName, headSHA)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create branch %s: %v", branchName, err)
		}
	}
	
	return nil
}