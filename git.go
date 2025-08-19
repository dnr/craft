package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v74/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

type GHConfig struct {
	GithubCom struct {
		OauthToken string `yaml:"oauth_token"`
		User       string `yaml:"user"`
	} `yaml:"github.com"`
}

func getGitHubToken() string {
	// First try GITHUB_TOKEN environment variable
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}

	// Try to read from gh CLI config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	configPath := filepath.Join(homeDir, ".config", "gh", "hosts.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}

	var config GHConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return ""
	}

	return config.GithubCom.OauthToken
}

func createGitHubClient() *github.Client {
	token := getGitHubToken()
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: No GitHub token found")
		fmt.Fprintln(os.Stderr, "Either:")
		fmt.Fprintln(os.Stderr, "  1. Set GITHUB_TOKEN environment variable")
		fmt.Fprintln(os.Stderr, "  2. Run 'gh auth login' to configure gh CLI")
		os.Exit(1)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc)
}

func getCurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func getRemoteName() string {
	// Check git config for craft.remoteName
	cmd := exec.Command("git", "config", "craft.remoteName")
	output, err := cmd.Output()
	if err == nil && len(strings.TrimSpace(string(output))) > 0 {
		return strings.TrimSpace(string(output))
	}

	// Default to origin
	return "origin"
}

func getRepoInfo() (owner, repo string, err error) {
	remoteName := getRemoteName()

	cmd := exec.Command("git", "remote", "get-url", remoteName)
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("not in a git repository or no '%s' remote", remoteName)
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
		return "", "", fmt.Errorf("remote '%s' is not a GitHub repository", remoteName)
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

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if len(line) < 2 {
			continue
		}

		// Check first two characters for status
		// ' M' = modified, not staged
		// 'M ' = modified, staged
		// 'MM' = modified, staged and unstaged
		// 'A ' = added, staged
		// '??' = untracked (allowed)
		status := line[:2]
		if status != "??" && strings.TrimSpace(status) != "" {
			return true // Has changes to tracked files or staged changes
		}
	}

	return false
}

func checkoutPRBranch(branchName, headRef, headSHA string) error {
	remoteName := getRemoteName()

	// Check if local branch already exists
	cmd := exec.Command("git", "rev-parse", "--verify", branchName)
	if cmd.Run() == nil {
		// Branch exists, switch to it
		cmd = exec.Command("git", "checkout", branchName)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to checkout existing branch %s: %v", branchName, err)
		}

		// Simply reset to the exact SHA we want (no need to fetch the branch)
		cmd = exec.Command("git", "reset", "--hard", headSHA)
		if err := cmd.Run(); err != nil {
			// If reset fails, the SHA might not be available locally, try fetching
			cmd = exec.Command("git", "fetch", remoteName)
			if fetchErr := cmd.Run(); fetchErr != nil {
				return fmt.Errorf("failed to fetch from remote and reset failed: reset=%v, fetch=%v", err, fetchErr)
			}

			// Try reset again
			cmd = exec.Command("git", "reset", "--hard", headSHA)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to reset to commit %s: %v", headSHA, err)
			}
		}
	} else {
		// Branch doesn't exist, create it from the SHA
		cmd = exec.Command("git", "checkout", "-b", branchName, headSHA)
		if err := cmd.Run(); err != nil {
			// If checkout fails, the SHA might not be available locally, try fetching
			cmd = exec.Command("git", "fetch", remoteName)
			if fetchErr := cmd.Run(); fetchErr != nil {
				return fmt.Errorf("failed to fetch from remote and checkout failed: checkout=%v, fetch=%v", err, fetchErr)
			}

			// Try checkout again
			cmd = exec.Command("git", "checkout", "-b", branchName, headSHA)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to create branch %s from %s: %v", branchName, headSHA, err)
			}
		}
	}

	return nil
}

func commitEmbeddedComments(prNumber int) error {
	// Add all modified files and PR-COMMENTS.txt if it exists
	cmd := exec.Command("git", "add", "-u") // Only add tracked files that were modified
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage changes: %v", err)
	}

	// Also add PR-COMMENTS.txt if it exists (might be new)
	if _, err := os.Stat(PRCommentsFile); err == nil {
		cmd = exec.Command("git", "add", PRCommentsFile)
		cmd.Run() // Ignore errors, might already be staged
	}

	// Check if there are any changes to commit
	cmd = exec.Command("git", "diff", "--cached", "--quiet")
	if cmd.Run() == nil {
		// No changes staged, nothing to commit
		return nil
	}

	// Commit with descriptive message
	commitMsg := fmt.Sprintf(CommitMsgTemplate, prNumber)
	cmd = exec.Command("git", "commit", "-m", commitMsg)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit: %v", err)
	}

	fmt.Printf("Committed embedded comments for PR #%d\n", prNumber)
	return nil
}
