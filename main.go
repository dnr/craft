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
		if err == nil && strings.HasPrefix(currentBranch, "pr-") {
			prNumberStr := strings.TrimPrefix(currentBranch, "pr-")
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
	branchName := fmt.Sprintf("pr-%d", prNumber)
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
	Author    string
	Body      string
	CreatedAt *time.Time
	IsNew     bool // True if this is a new comment to be submitted
}

type FileWithComments struct {
	Path     string
	Lines    []string
	Comments map[int][]ReviewComment // Line number -> comments
}

func (f *FileWithComments) Parse(content string) error {
	lines := strings.Split(content, "\n")
	f.Lines = make([]string, 0)
	f.Comments = make(map[int][]ReviewComment)
	
	commentPrefix := getCommentPrefix(f.Path)
	
	var pendingComments []ReviewComment
	
	for _, line := range lines {
		if strings.Contains(line, " ⦒ ") && strings.HasPrefix(strings.TrimSpace(line), commentPrefix) {
			// This is an embedded comment
			content := strings.TrimSpace(line)
			if idx := strings.Index(content, " ⦒ "); idx != -1 {
				commentContent := content[idx+3:] // Skip " ⦒ "
				
				// Parse comment format: "author: body" or "+: body" for new comments
				var comment ReviewComment
				if strings.HasPrefix(commentContent, "+: ") {
					comment.Author = ""
					comment.Body = commentContent[3:]
					comment.IsNew = true
					pendingComments = append(pendingComments, comment)
				} else if colonIdx := strings.Index(commentContent, ": "); colonIdx != -1 {
					comment.Author = commentContent[:colonIdx]
					comment.Body = commentContent[colonIdx+2:]
					comment.IsNew = false
					pendingComments = append(pendingComments, comment)
				} else {
					// This might be a continuation line of a multi-line comment
					if len(pendingComments) > 0 {
						// Add to the body of the last comment
						lastIdx := len(pendingComments) - 1
						if pendingComments[lastIdx].Body == "" {
							pendingComments[lastIdx].Body = commentContent
						} else {
							pendingComments[lastIdx].Body += " " + commentContent
						}
					}
				}
			}
		} else {
			// This is a source code line
			f.Lines = append(f.Lines, line)
			
			// If we have pending comments, attach them to this line
			if len(pendingComments) > 0 {
				lineNum := len(f.Lines) // 1-based line number for the line we just added
				f.Comments[lineNum] = append(f.Comments[lineNum], pendingComments...)
				pendingComments = nil
			}
		}
	}
	
	return nil
}

func getIndentation(line string) string {
	var indent strings.Builder
	for _, r := range line {
		if r == ' ' || r == '\t' {
			indent.WriteRune(r)
		} else {
			break
		}
	}
	return indent.String()
}

func wrapText(text string, width int, indent string) []string {
	if len(text) <= width {
		return []string{text}
	}
	
	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	
	currentLine := words[0]
	for _, word := range words[1:] {
		// Account for indent + comment prefix + space
		if len(currentLine)+len(word)+1 <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	
	return lines
}

func (f *FileWithComments) Serialize() string {
	var result strings.Builder
	commentPrefix := getCommentPrefix(f.Path)
	
	for i, line := range f.Lines {
		result.WriteString(line)
		
		// Add any comments for this line (after the line)
		if comments, exists := f.Comments[i+1]; exists { // GitHub uses 1-based line numbers
			indent := getIndentation(line)
			
			for _, comment := range comments {
				result.WriteString("\n")
				
				if comment.IsNew {
					// New comment format: just the body with +: prefix
					result.WriteString(indent + commentPrefix + " ⦒ +: " + comment.Body)
				} else {
					// Existing comment with header and wrapped body
					dateStr := ""
					if comment.CreatedAt != nil {
						dateStr = comment.CreatedAt.Format("2006-01-02 15:04")
					}
					
					// Header line with horizontal rule (5 dashes before, rest after)
					headerText := comment.Author
					if dateStr != "" {
						headerText += " (" + dateStr + ")"
					}
					headerText += ":"
					
					// Calculate remaining space for trailing dashes
					prefixLen := len(indent + commentPrefix + " ⦒ ───── ")
					trailingDashCount := 100 - prefixLen - len(headerText) - 1 // -1 for space before trailing dashes
					if trailingDashCount < 3 {
						trailingDashCount = 3 // Minimum trailing dashes
					}
					
					result.WriteString(indent + commentPrefix + " ⦒ ───── " + headerText + " " + strings.Repeat("─", trailingDashCount))
					
					// Wrapped body lines
					bodyWidth := 100 - len(indent) - len(commentPrefix) - 3 // " ⦒ "
					if bodyWidth < 20 {
						bodyWidth = 20 // Minimum reasonable width
					}
					
					wrappedLines := wrapText(comment.Body, bodyWidth, indent)
					for _, wrappedLine := range wrappedLines {
						result.WriteString("\n")
						result.WriteString(indent + commentPrefix + " ⦒ " + wrappedLine)
					}
				}
			}
		}
		
		// Add newline after line (and any comments)
		if i < len(f.Lines)-1 {
			result.WriteString("\n")
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
		comment := ReviewComment{
			ID:        ghComment.GetID(),
			Line:      lineNum,
			Author:    ghComment.GetUser().GetLogin(),
			Body:      ghComment.GetBody(),
			CreatedAt: ghComment.CreatedAt.GetTime(),
			IsNew:     false,
		}
		f.Comments[lineNum] = append(f.Comments[lineNum], comment)
	}
}

func embedPRComments(client *github.Client, ctx context.Context, owner, repo string, prNumber int) error {
	// Get PR review comments (inline comments on code)
	comments, _, err := client.PullRequests.ListComments(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch PR comments: %v", err)
	}
	
	// Group comments by file
	fileComments := make(map[string][]*github.PullRequestComment)
	for _, comment := range comments {
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
	
	fmt.Printf("Embedded comments in %d files\n", len(fileComments))
	return nil
}

func processFileComments(filePath string, ghComments []*github.PullRequestComment) error {
	// Read existing file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	
	// Parse into intermediate representation
	fileWithComments := &FileWithComments{Path: filePath}
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

func getCommentPrefix(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go", ".js", ".ts", ".tsx", ".jsx", ".c", ".cpp", ".h", ".hpp", ".java", ".rs", ".php":
		return "//"
	case ".py", ".sh", ".rb", ".yaml", ".yml":
		return "#"
	case ".html", ".xml":
		return "<!--"
	case ".css", ".scss", ".less":
		return "/*"
	default:
		return "#" // Default fallback
	}
}

func commitEmbeddedComments(prNumber int) error {
	// Add all modified files
	cmd := exec.Command("git", "add", "-u") // Only add tracked files that were modified
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage changes: %v", err)
	}
	
	// Check if there are any changes to commit
	cmd = exec.Command("git", "diff", "--cached", "--quiet")
	if cmd.Run() == nil {
		// No changes staged, nothing to commit
		return nil
	}
	
	// Commit with descriptive message
	commitMsg := fmt.Sprintf("craft: embed PR #%d review comments", prNumber)
	cmd = exec.Command("git", "commit", "-m", commitMsg)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit: %v", err)
	}
	
	fmt.Printf("Committed embedded comments for PR #%d\n", prNumber)
	return nil
}