package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"
)

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

func (r *ReviewComment) Format() string {
	// Format: ───── author ─ date ─ metadata ──────────

	metadata := ""
	if r.IsFile {
		metadata = " [file]"
	} else if r.StartLine > 0 && r.StartLine < r.Line {
		rangeSize := r.Line - r.StartLine + 1
		metadata = fmt.Sprintf(" [-%d]", rangeSize)
	}

	parts := []string{r.Author}

	if r.CreatedAt != nil {
		parts = append(parts, r.CreatedAt.Format(TimeFormat))
	}

	if metadata != "" {
		parts = append(parts, strings.TrimSpace(metadata))
	}

	return strings.Join(parts, " "+RuleChar+" ")
}

// orderComments returns comments with existing comments first, then new comments
func orderComments(comments []ReviewComment) []ReviewComment {
	existingComments := []ReviewComment{}
	newComments := []ReviewComment{}

	for _, comment := range comments {
		if comment.IsNew {
			newComments = append(newComments, comment)
		} else {
			existingComments = append(existingComments, comment)
		}
	}

	return append(existingComments, newComments...)
}

// renderCommentWithHeader renders a comment with header and wrapped body
func renderCommentWithHeader(comment ReviewComment, prefixLen int, bodyWidth int, prefix string) string {
	var result strings.Builder

	// Create display comment with NEW author for new comments
	displayComment := comment
	if comment.IsNew {
		displayComment.Author = "NEW"
	}

	headerText := displayComment.Format()
	leadingDashes := LeadingDashes
	if prefixLen == 0 {
		leadingDashes = 7 // For PR comments
	}
	rule := createHorizontalRule(prefixLen, headerText, leadingDashes)
	result.WriteString(rule)

	// Wrapped body lines
	if bodyWidth <= 0 {
		bodyWidth = MaxLineLength
	}
	if bodyWidth < 20 {
		bodyWidth = 20 // Minimum reasonable width
	}

	wrappedLines := wrapText(comment.Body, bodyWidth, "")
	for _, wrappedLine := range wrappedLines {
		result.WriteString("\n")
		result.WriteString(prefix + wrappedLine)
	}

	return result.String()
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
		isStopMarker := false
		commentContent := ""

		if f.IsPRComments() {
			// For PR comments file: check for rule headers
			rulePrefix := strings.Repeat(RuleChar, 7)
			if strings.HasPrefix(trimmed, rulePrefix) {
				// This is a comment header line
				isCommentLine = true
				commentContent = strings.TrimLeft(trimmed, RuleChar+" ")
			} else if currentComment != nil && trimmed != "" {
				// This is a continuation line of a comment body
				isCommentLine = true
				commentContent = trimmed
			}
		} else {
			// For source code files: check for shorthand syntax first, then embedded comments
			shorthandStart := languageComment + "++"
			shorthandStop := languageComment + "--"

			if strings.HasPrefix(trimmed, shorthandStart) {
				// Start of shorthand new comment - save previous comment if exists
				if currentComment != nil {
					pendingComments = append(pendingComments, *currentComment)
				}
				currentComment = &ReviewComment{
					IsNew:  true,
					Body:   strings.TrimSpace(trimmed[len(shorthandStart):]),
					Author: "",
				}
				// This line should not be included in source
				isStopMarker = true // Mark as consumed so it doesn't get added to source lines
			} else if strings.HasPrefix(trimmed, shorthandStop) {
				// Stop shorthand comment parsing - finish current comment if any
				if currentComment != nil && currentComment.IsNew {
					pendingComments = append(pendingComments, *currentComment)
					currentComment = nil
				}
				// This line should not be included in source
				isCommentLine = false
				isStopMarker = true
			} else if currentComment != nil && currentComment.IsNew && strings.HasPrefix(trimmed, languageComment) {
				// Continuation of shorthand comment - check if it's a regular comment line
				potentialContent := strings.TrimSpace(trimmed[len(languageComment):])
				if potentialContent != "" {
					// Add to current comment body
					if currentComment.Body != "" {
						currentComment.Body += "\n"
					}
					currentComment.Body += potentialContent
				}
				// This line should not be included in source
				isStopMarker = true
			} else if strings.Contains(line, " "+CraftMarker+" ") && strings.HasPrefix(trimmed, languageComment) {
				// Regular embedded comments with craft marker
				isCommentLine = true
				markerWithSpaces := " " + CraftMarker + " "
				if idx := strings.Index(trimmed, markerWithSpaces); idx != -1 {
					commentContent = trimmed[idx+len(markerWithSpaces):]
				}
			}
		}

		if isCommentLine && commentContent != "" {
			// Parse comment content
			if strings.Contains(commentContent, " "+RuleChar+" ") {
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
		} else if !f.IsPRComments() && !isStopMarker {
			// This is a source code line (only for non-PR files, and not a stop marker)
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
		orderedComments := orderComments(f.Comments[0])

		for i, comment := range orderedComments {
			if i > 0 {
				result.WriteString("\n\n")
			}

			rendered := renderCommentWithHeader(comment, 0, MaxLineLength, "")
			result.WriteString(rendered + "\n")
		}
	} else {
		// For source code files: serialize with comment prefixes
		for i, line := range f.Lines {
			result.WriteString(line)

			// Add any comments for this line (after the line)
			indent := getIndentation(line)
			orderedComments := orderComments(f.Comments[i+1]) // GitHub uses 1-based line numbers

			for _, comment := range orderedComments {
				result.WriteString("\n")

				// Calculate dimensions for comment rendering
				markerSpace := " " + CraftMarker + " "
				commentPrefix := indent + languageComment + markerSpace
				prefixLen := len(commentPrefix)
				bodyWidth := MaxLineLength - len(indent) - len(languageComment) - len(markerSpace)
				bodyPrefix := indent + languageComment + markerSpace

				rendered := renderCommentWithHeader(comment, prefixLen, bodyWidth, bodyPrefix)
				result.WriteString(commentPrefix + rendered)
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
