package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VCS abstracts version control operations for git and jj.
type VCS interface {
	// Name returns "git" or "jj"
	Name() string

	// Root returns the repository root directory
	Root() string

	// HasUncommittedChanges returns true if there are uncommitted changes
	HasUncommittedChanges() (bool, error)

	// FetchPRBranch fetches the PR branch from the remote
	FetchPRBranch(remote string, prNumber int) error

	// CreateAndSwitchBranch creates a local branch for the PR and switches to it.
	// If the branch exists, it resets it to the fetched PR head.
	CreateAndSwitchBranch(prNumber int, commitOID string) error

	// Commit creates a commit with the given message.
	// In jj, this creates a new change on top of the current one.
	Commit(message string) error

	// GetRemoteURL returns the URL of the given remote
	GetRemoteURL(remote string) (string, error)

	// GetCurrentBranch returns the current branch name (or bookmark in jj)
	GetCurrentBranch() (string, error)

	// GetConfigValue returns a git/jj config value
	GetConfigValue(key string) (string, error)
}

// DetectVCS detects whether the current directory is a git or jj repo.
func DetectVCS(dir string) (VCS, error) {
	// Check for jj first (it can colocate with git)
	if _, err := os.Stat(filepath.Join(dir, ".jj")); err == nil {
		return &JJRepo{root: dir}, nil
	}

	// Check for git
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return &GitRepo{root: strings.TrimSpace(string(out))}, nil
	}

	return nil, fmt.Errorf("not a git or jj repository")
}

// GitRepo implements VCS for git repositories.
type GitRepo struct {
	root string
}

func (g *GitRepo) Name() string { return "git" }
func (g *GitRepo) Root() string { return g.root }

func (g *GitRepo) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.root
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *GitRepo) runNoOutput(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (g *GitRepo) HasUncommittedChanges() (bool, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

func (g *GitRepo) FetchPRBranch(remote string, prNumber int) error {
	// Fetch the PR head ref
	refspec := fmt.Sprintf("refs/pull/%d/head", prNumber)
	return g.runNoOutput("fetch", remote, refspec)
}

func (g *GitRepo) CreateAndSwitchBranch(prNumber int, commitOID string) error {
	branchName := fmt.Sprintf("pr-%d", prNumber)
	return g.runNoOutput("switch", "-C", branchName, commitOID)
}

func (g *GitRepo) Commit(message string) error {
	// Stage all changes
	if err := g.runNoOutput("add", "-A"); err != nil {
		return err
	}
	// Commit (allow empty in case nothing changed)
	return g.runNoOutput("commit", "--allow-empty", "-m", message)
}

func (g *GitRepo) GetRemoteURL(remote string) (string, error) {
	return g.run("remote", "get-url", remote)
}

func (g *GitRepo) GetCurrentBranch() (string, error) {
	return g.run("rev-parse", "--abbrev-ref", "HEAD")
}

func (g *GitRepo) GetConfigValue(key string) (string, error) {
	return g.run("config", "--get", key)
}

// JJRepo implements VCS for jj repositories.
type JJRepo struct {
	root string
}

func (j *JJRepo) Name() string { return "jj" }
func (j *JJRepo) Root() string { return j.root }

func (j *JJRepo) run(args ...string) (string, error) {
	cmd := exec.Command("jj", args...)
	cmd.Dir = j.root
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("jj %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (j *JJRepo) runNoOutput(args ...string) error {
	cmd := exec.Command("jj", args...)
	cmd.Dir = j.root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (j *JJRepo) runGitNoOutput(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = j.root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (j *JJRepo) HasUncommittedChanges() (bool, error) {
	// There is no such thing in jj.
	return false, nil
}

func (j *JJRepo) FetchPRBranch(remote string, prNumber int) error {
	refspec := fmt.Sprintf("refs/pull/%d/head:pr-%d", prNumber, prNumber)
	err := j.runGitNoOutput("fetch", "--force", remote, refspec)
	if err != nil {
		return err
	}
	return j.runNoOutput("git", "import")
}

func (j *JJRepo) CreateAndSwitchBranch(prNumber int, commitOID string) error {
	bookmarkName := fmt.Sprintf("pr-%d", prNumber)

	// Set or move the bookmark to the new change
	// First try to move existing bookmark, if that fails, create it
	if err := j.runNoOutput("bookmark", "set", "--allow-backwards", "-r", commitOID, bookmarkName); err != nil {
		// Bookmark might not exist, try create
		err = j.runNoOutput("bookmark", "create", "-r", commitOID, bookmarkName)
		if err != nil {
			return err
		}
	}

	// Create a new change at the commit
	return j.runNoOutput("new", commitOID)
}

func (j *JJRepo) Commit(message string) error {
	// In jj, we describe the current change and then create a new one
	if err := j.runNoOutput("describe", "-m", message); err != nil {
		return err
	}
	// Create a new empty change on top
	return j.runNoOutput("new")
}

func (j *JJRepo) GetRemoteURL(remote string) (string, error) {
	// jj stores git remote info, we can use git config
	cmd := exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = j.root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (j *JJRepo) GetCurrentBranch() (string, error) {
	// Get bookmarks pointing to current change
	return j.run("log", "-r", "heads(bookmarks(glob:'pr-*') & ..@)", "--no-graph",
		"-T", "bookmarks.map(|b| if(b.name().starts_with('pr-'),b.name())).join('')")
}

func (j *JJRepo) GetConfigValue(key string) (string, error) {
	// Try jj config first, fall back to git config
	out, err := j.run("config", "get", key)
	if err == nil {
		return out, nil
	}
	// Fall back to git config for things like craft.remoteName
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = j.root
	gitOut, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(gitOut)), nil
}

// ParseGitHubRemote extracts owner and repo from a GitHub remote URL.
func ParseGitHubRemote(url string) (owner, repo string, err error) {
	// Handle SSH format: git@github.com:owner/repo.git
	if strings.HasPrefix(url, "git@github.com:") {
		path := strings.TrimPrefix(url, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid GitHub SSH URL: %s", url)
		}
		return parts[0], parts[1], nil
	}

	// Handle HTTPS format: https://github.com/owner/repo.git
	if strings.HasPrefix(url, "https://github.com/") {
		path := strings.TrimPrefix(url, "https://github.com/")
		path = strings.TrimSuffix(path, ".git")
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid GitHub HTTPS URL: %s", url)
		}
		return parts[0], parts[1], nil
	}

	return "", "", fmt.Errorf("not a GitHub URL: %s", url)
}
