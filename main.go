package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
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

type ReviewComment struct {
	ID        int64
	Line      int
	StartLine int // For range comments, the starting line (0 if not a range)
	Author    string
	Body      string
	CreatedAt *time.Time
	IsNew     bool // True if this is a new comment to be submitted
	IsFile    bool // True if this is a file-level comment
}

type FileWithComments struct {
	Path     string
	Lines    []string
	Comments map[int][]ReviewComment // Line number -> comments
}

func NewFileWithComments(path string) *FileWithComments {
	return &FileWithComments{
		Path:     path,
		Lines:    make([]string, 0),
		Comments: make(map[int][]ReviewComment),
	}
}

func NewPRComments() *FileWithComments {
	// PR comments are just FileWithComments with no source lines and no comment prefix
	return NewFileWithComments(PRCommentsFile)
}

func (f *FileWithComments) Parse(content string) error {
	lines := strings.Split(content, "\n")
	f.Lines = make([]string, 0)
	f.Comments = make(map[int][]ReviewComment)

	languageComment := getLanguageCommentPrefix(f.Path)

	var pendingComments []ReviewComment
	var currentComment *ReviewComment

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isCommentLine := false
		commentContent := ""

		if f.IsPRComments() {
			// For PR comments file: check for rule headers or +: lines
			rulePrefix := strings.Repeat(RuleChar, 7)
			if strings.HasPrefix(trimmed, rulePrefix) {
				// This is a comment header line
				isCommentLine = true
				commentContent = strings.TrimLeft(trimmed, RuleChar+" ")
			} else if strings.HasPrefix(trimmed, NewCommentPrefix) {
				// This is a new comment line
				isCommentLine = true
				commentContent = NewCommentPrefix + strings.TrimPrefix(trimmed, NewCommentPrefix)
			} else if currentComment != nil && trimmed != "" {
				// This is a continuation line of a comment body
				isCommentLine = true
				commentContent = trimmed
			}
		} else {
			// For source code files: check for embedded comments with prefix
			if strings.Contains(line, " "+CraftMarker+" ") && strings.HasPrefix(trimmed, languageComment) {
				isCommentLine = true
				markerWithSpaces := " " + CraftMarker + " "
				if idx := strings.Index(trimmed, markerWithSpaces); idx != -1 {
					commentContent = trimmed[idx+len(markerWithSpaces):]
				}
			}
		}

		if isCommentLine && commentContent != "" {
			// Parse comment content
			if strings.HasPrefix(commentContent, NewCommentPrefix) {
				// Save previous comment if exists
				if currentComment != nil {
					pendingComments = append(pendingComments, *currentComment)
				}
				currentComment = &ReviewComment{
					IsNew:  true,
					Body:   commentContent[len(NewCommentPrefix):],
					Author: "",
				}
			} else if strings.Contains(commentContent, " "+RuleChar+" ") {
				// Save previous comment if exists
				if currentComment != nil {
					pendingComments = append(pendingComments, *currentComment)
				}
				// Parse header with rule character separators
				currentComment = &ReviewComment{
					IsNew: false,
					Body:  "",
				}

				// For source files, strip leading rule characters first
				headerContent := commentContent
				// Strip any number of leading rule characters and space
				headerContent = strings.TrimLeft(headerContent, RuleChar+" ")

				// Split by rule character separator
				parts := strings.Split(headerContent, " "+RuleChar+" ")
				if len(parts) >= 1 {
					currentComment.Author = strings.TrimSpace(parts[0])
				}
				if len(parts) >= 2 {
					dateStr := strings.TrimSpace(parts[1])
					if t, err := time.Parse(TimeFormat, dateStr); err == nil {
						currentComment.CreatedAt = &t
					}
				}
				if len(parts) >= 3 {
					metadata := strings.TrimSpace(parts[2])
					if metadata == "[file]" {
						currentComment.IsFile = true
					} else if strings.HasPrefix(metadata, "[-") && strings.HasSuffix(metadata, "]") {
						// Parse range metadata like [-3]
						rangeStr := metadata[2 : len(metadata)-1]
						if rangeSize, err := strconv.Atoi(rangeStr); err == nil && rangeSize > 0 {
							// We'll need to set StartLine when we know the current line
							currentComment.StartLine = -rangeSize // Temporary marker
						}
					}
				}
			} else if currentComment != nil {
				// This is a continuation line
				if currentComment.Body != "" {
					currentComment.Body += "\n"
				}
				currentComment.Body += commentContent
			}
		} else if !f.IsPRComments() {
			// This is a source code line (only for non-PR files)
			f.Lines = append(f.Lines, line)

			// If we have pending comments, attach them to this line
			if len(pendingComments) > 0 || currentComment != nil {
				if currentComment != nil {
					pendingComments = append(pendingComments, *currentComment)
					currentComment = nil
				}
				lineNum := len(f.Lines) // 1-based line number for the line we just added

				// Fix up range comments - convert negative StartLine markers to actual line numbers
				for i := range pendingComments {
					if pendingComments[i].StartLine < 0 {
						rangeSize := -pendingComments[i].StartLine
						pendingComments[i].StartLine = lineNum - rangeSize + 1
						pendingComments[i].Line = lineNum
					} else {
						pendingComments[i].Line = lineNum
					}
				}

				f.Comments[lineNum] = append(f.Comments[lineNum], pendingComments...)
				pendingComments = nil
			}
		}
	}

	// Handle any remaining comments (for PR files, attach to line 0)
	if len(pendingComments) > 0 || currentComment != nil {
		if currentComment != nil {
			pendingComments = append(pendingComments, *currentComment)
		}
		attachLine := 0
		if !f.IsPRComments() && len(f.Lines) > 0 {
			attachLine = len(f.Lines) // Attach to last line if it's a source file
		}

		// Fix up range comments for remaining comments too
		for i := range pendingComments {
			if pendingComments[i].StartLine < 0 && !f.IsPRComments() {
				rangeSize := -pendingComments[i].StartLine
				pendingComments[i].StartLine = attachLine - rangeSize + 1
				pendingComments[i].Line = attachLine
			} else {
				pendingComments[i].Line = attachLine
			}
		}

		f.Comments[attachLine] = append(f.Comments[attachLine], pendingComments...)
	}

	return nil
}

func (f *FileWithComments) IsPRComments() bool {
	return f.Path == PRCommentsFile
}

func (f *FileWithComments) Serialize() string {
	var result strings.Builder
	languageComment := getLanguageCommentPrefix(f.Path)

	if f.IsPRComments() {
		// For PR comments file: serialize comments at line 0
		for i, comment := range f.Comments[0] {
			if i > 0 {
				result.WriteString("\n\n")
			}

			if comment.IsNew {
				result.WriteString(NewCommentPrefix + comment.Body + "\n")
			} else {
				headerText := formatCommentHeader(comment.Author, comment.CreatedAt, "")
				rule := createHorizontalRule(0, headerText, 7)
				result.WriteString(rule + "\n")

				// Wrap and write body
				wrappedLines := wrapText(comment.Body, MaxLineLength, "")
				for _, line := range wrappedLines {
					result.WriteString(line + "\n")
				}
			}
		}
	} else {
		// For source code files: serialize with comment prefixes
		for i, line := range f.Lines {
			result.WriteString(line)

			// Add any comments for this line (after the line)
			indent := getIndentation(line)

			for _, comment := range f.Comments[i+1] { // GitHub uses 1-based line numbers
				result.WriteString("\n")

				if comment.IsNew {
					// New comment format: just the body with +: prefix
					result.WriteString(indent + languageComment + " " + CraftMarker + " " + NewCommentPrefix + comment.Body)
				} else {
					// Existing comment with header and wrapped body
					metadata := ""
					if comment.IsFile {
						metadata = " [file]"
					} else if comment.StartLine > 0 && comment.StartLine < comment.Line {
						rangeSize := comment.Line - comment.StartLine + 1
						metadata = fmt.Sprintf(" [-%d]", rangeSize)
					}

					headerText := formatCommentHeader(comment.Author, comment.CreatedAt, metadata)
					prefixLen := len(indent + languageComment + " " + CraftMarker + " ")
					rule := createHorizontalRule(prefixLen, headerText, LeadingDashes)

					result.WriteString(indent + languageComment + " " + CraftMarker + " " + rule)

					// Wrapped body lines
					markerSpace := " " + CraftMarker + " "
					bodyWidth := MaxLineLength - len(indent) - len(languageComment) - len(markerSpace)
					if bodyWidth < 20 {
						bodyWidth = 20 // Minimum reasonable width
					}

					wrappedLines := wrapText(comment.Body, bodyWidth, indent)
					for _, wrappedLine := range wrappedLines {
						result.WriteString("\n")
						result.WriteString(indent + languageComment + markerSpace + wrappedLine)
					}
				}
			}

			// Add newline after line (and any comments)
			if i < len(f.Lines)-1 {
				result.WriteString("\n")
			}
		}
	}

	return result.String()
}

func (f *FileWithComments) SyncWithGitHubComments(ghComments []*github.PullRequestComment) {
	// Clear existing non-new comments
	for lineNum, comments := range f.Comments {
		var newComments []ReviewComment
		for _, comment := range comments {
			if comment.IsNew {
				newComments = append(newComments, comment)
			}
		}
		if len(newComments) > 0 {
			f.Comments[lineNum] = newComments
		} else {
			delete(f.Comments, lineNum)
		}
	}

	// Add GitHub comments
	for _, ghComment := range ghComments {
		lineNum := ghComment.GetLine()
		startLine := 0
		if ghComment.StartLine != nil {
			startLine = ghComment.GetStartLine()
		}

		// Detect file-level comments (GitHub sometimes puts them on line 0 or 1)
		isFile := (lineNum <= 1 && ghComment.GetPath() != "" && ghComment.GetDiffHunk() == "")

		comment := ReviewComment{
			ID:        ghComment.GetID(),
			Line:      lineNum,
			StartLine: startLine,
			Author:    ghComment.GetUser().GetLogin(),
			Body:      ghComment.GetBody(),
			CreatedAt: ghComment.CreatedAt.GetTime(),
			IsNew:     false,
			IsFile:    isFile,
		}

		// For file-level comments, always put them on line 1
		if isFile {
			comment.Line = 1
		}

		f.Comments[comment.Line] = append(f.Comments[comment.Line], comment)
	}
}

func (f *FileWithComments) SyncWithGitHubIssueComments(ghComments []*github.IssueComment) {
	// For PR-level comments - clear existing non-new comments at line 0
	var newComments []ReviewComment
	if comments, exists := f.Comments[0]; exists {
		for _, comment := range comments {
			if comment.IsNew {
				newComments = append(newComments, comment)
			}
		}
	}
	f.Comments[0] = newComments

	// Add GitHub issue comments at line 0
	for _, ghComment := range ghComments {
		comment := ReviewComment{
			ID:        ghComment.GetID(),
			Line:      0,
			Author:    ghComment.GetUser().GetLogin(),
			Body:      ghComment.GetBody(),
			CreatedAt: ghComment.CreatedAt.GetTime(),
			IsNew:     false,
		}
		f.Comments[0] = append(f.Comments[0], comment)
	}
}

func embedPRComments(client *github.Client, ctx context.Context, owner, repo string, prNumber int) error {
	// Get PR review comments (inline comments on code)
	reviewComments, _, err := client.PullRequests.ListComments(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch PR review comments: %v", err)
	}

	// Get PR-level comments (issue comments on the PR)
	prComments, _, err := client.Issues.ListComments(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch PR comments: %v", err)
	}

	// Process PR-level comments first
	if len(prComments) > 0 {
		err := processPRLevelComments(PRCommentsFile, prComments)
		if err != nil {
			return fmt.Errorf("failed to process PR comments: %v", err)
		}
	}

	// Group review comments by file
	fileComments := make(map[string][]*github.PullRequestComment)
	for _, comment := range reviewComments {
		if comment.GetPath() != "" {
			fileComments[comment.GetPath()] = append(fileComments[comment.GetPath()], comment)
		}
	}

	// Process each file with comments
	for filePath, comments := range fileComments {
		err := processFileComments(filePath, comments)
		if err != nil {
			return fmt.Errorf("failed to process comments in %s: %v", filePath, err)
		}
	}

	fmt.Printf("Embedded comments in %d files", len(fileComments))
	if len(prComments) > 0 {
		fmt.Printf(" + %d PR comments", len(prComments))
	}
	fmt.Println()
	return nil
}

func processFileComments(filePath string, ghComments []*github.PullRequestComment) error {
	// Read existing file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// Parse into intermediate representation
	fileWithComments := NewFileWithComments(filePath)
	err = fileWithComments.Parse(string(content))
	if err != nil {
		return err
	}

	// Sync with GitHub comments
	fileWithComments.SyncWithGitHubComments(ghComments)

	// Write back to disk
	serialized := fileWithComments.Serialize()
	return os.WriteFile(filePath, []byte(serialized), 0644)
}

func processPRLevelComments(filePath string, ghComments []*github.IssueComment) error {
	// Read existing PR comments file if it exists
	prComments := NewPRComments()
	if content, err := os.ReadFile(filePath); err == nil {
		prComments.Parse(string(content))
	}

	// Sync with GitHub comments
	prComments.SyncWithGitHubIssueComments(ghComments)

	// Write back to disk
	return os.WriteFile(filePath, []byte(prComments.Serialize()), 0644)
}

func formatCommentHeader(author string, createdAt *time.Time, metadata string) string {
	// Format: ───── author ─ date ─ metadata ──────────
	parts := []string{author}

	if createdAt != nil {
		parts = append(parts, createdAt.Format(TimeFormat))
	}

	if metadata != "" {
		parts = append(parts, strings.TrimSpace(metadata))
	}

	return strings.Join(parts, " "+RuleChar+" ")
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
