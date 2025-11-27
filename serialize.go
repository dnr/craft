package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	craftPrefix    = "❯"
	headerStart    = "─────"
	headerFieldSep = " ─ "
	prStateFile    = "PR-STATE.txt"
)

// commentStyle defines how comments work for a language.
type commentStyle struct {
	linePrefix string // e.g., "//" or "#"
}

var commentStyles = map[string]commentStyle{
	".go":   {linePrefix: "//"},
	".js":   {linePrefix: "//"},
	".ts":   {linePrefix: "//"},
	".tsx":  {linePrefix: "//"},
	".jsx":  {linePrefix: "//"},
	".java": {linePrefix: "//"},
	".c":    {linePrefix: "//"},
	".cpp":  {linePrefix: "//"},
	".cc":   {linePrefix: "//"},
	".h":    {linePrefix: "//"},
	".hpp":  {linePrefix: "//"},
	".rs":   {linePrefix: "//"},
	".swift": {linePrefix: "//"},
	".kt":   {linePrefix: "//"},
	".scala": {linePrefix: "//"},
	".py":   {linePrefix: "#"},
	".rb":   {linePrefix: "#"},
	".sh":   {linePrefix: "#"},
	".bash": {linePrefix: "#"},
	".zsh":  {linePrefix: "#"},
	".pl":   {linePrefix: "#"},
	".pm":   {linePrefix: "#"},
	".r":    {linePrefix: "#"},
	".R":    {linePrefix: "#"},
	".yaml": {linePrefix: "#"},
	".yml":  {linePrefix: "#"},
	".toml": {linePrefix: "#"},
	".tf":   {linePrefix: "#"},
	".lua":  {linePrefix: "--"},
	".hs":   {linePrefix: "--"},
	".sql":  {linePrefix: "--"},
	".elm":  {linePrefix: "--"},
	".erl":  {linePrefix: "%"},
	".ex":   {linePrefix: "#"},
	".exs":  {linePrefix: "#"},
	".clj":  {linePrefix: ";;"},
	".lisp": {linePrefix: ";;"},
	".scm":  {linePrefix: ";;"},
	".vim":  {linePrefix: "\""},
	".el":   {linePrefix: ";;"},
}

func getCommentStyle(path string) commentStyle {
	ext := filepath.Ext(path)
	if style, ok := commentStyles[ext]; ok {
		return style
	}
	// Default to // for unknown extensions
	return commentStyle{linePrefix: "//"}
}

// formatCraftLine formats a line of craft content for a source file.
func formatCraftLine(linePrefix, content string) string {
	return fmt.Sprintf("%s %s %s", linePrefix, craftPrefix, content)
}

// Header represents a parsed comment header.
type Header struct {
	Author    string
	Timestamp time.Time
	NodeID    string // Full node ID like "PRRC_kwDOPgi5ks6ZBMOo"
	IsNew     bool
	IsFile    bool // file-level comment
	Range     int  // negative number for range comments (e.g., -12 means 12 lines above)
	IsThread  bool // explicit new thread marker (for multiple threads on same line)
}

// formatNodeID converts a full node ID to the short format for headers.
// "PRRC_kwDOPgi5ks6ZBMOo" -> "prrc kwDOPgi5ks6ZBMOo"
func formatNodeID(id string) string {
	if id == "" {
		return ""
	}
	idx := strings.Index(id, "_")
	if idx == -1 {
		return strings.ToLower(id)
	}
	prefix := strings.ToLower(id[:idx])
	suffix := id[idx+1:]
	return prefix + " " + suffix
}

// parseNodeID converts the short format back to full node ID.
// "prrc kwDOPgi5ks6ZBMOo" -> "PRRC_kwDOPgi5ks6ZBMOo"
func parseNodeID(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.SplitN(s, " ", 2)
	if len(parts) == 1 {
		return strings.ToUpper(s)
	}
	return strings.ToUpper(parts[0]) + "_" + parts[1]
}

// formatHeader creates a header line from a Header struct.
func formatHeader(h Header) string {
	var fields []string

	if h.IsNew {
		fields = append(fields, "new")
	} else {
		if h.Author != "" {
			fields = append(fields, "by "+h.Author)
		}
		if !h.Timestamp.IsZero() {
			fields = append(fields, "at "+h.Timestamp.Format("2006-01-02 15:04"))
		}
	}

	if h.IsFile {
		fields = append(fields, "file")
	}

	if h.Range != 0 {
		fields = append(fields, fmt.Sprintf("range %d", h.Range))
	}

	if h.IsThread {
		fields = append(fields, "thread")
	}

	if h.NodeID != "" {
		fields = append(fields, formatNodeID(h.NodeID))
	}

	return headerStart + " " + strings.Join(fields, headerFieldSep) + " " + headerStart
}

// parseHeader parses a header line into a Header struct.
func parseHeader(line string) (Header, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, headerStart) || !strings.HasSuffix(line, headerStart) {
		return Header{}, false
	}

	// Strip the delimiters
	content := strings.TrimPrefix(line, headerStart)
	content = strings.TrimSuffix(content, headerStart)
	content = strings.TrimSpace(content)

	if content == "" {
		return Header{}, false
	}

	h := Header{}
	fields := strings.Split(content, headerFieldSep)

	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}

		switch {
		case field == "new":
			h.IsNew = true
		case field == "file":
			h.IsFile = true
		case field == "thread":
			h.IsThread = true
		case strings.HasPrefix(field, "by "):
			h.Author = strings.TrimPrefix(field, "by ")
		case strings.HasPrefix(field, "at "):
			ts := strings.TrimPrefix(field, "at ")
			if t, err := time.Parse("2006-01-02 15:04", ts); err == nil {
				h.Timestamp = t
			}
		case strings.HasPrefix(field, "range "):
			fmt.Sscanf(field, "range %d", &h.Range)
		case strings.HasPrefix(field, "prrc ") || strings.HasPrefix(field, "ic ") ||
			strings.HasPrefix(field, "prrt ") || strings.HasPrefix(field, "pr "):
			h.NodeID = parseNodeID(field)
		}
	}

	return h, true
}

// SerializeOptions configures serialization behavior.
type SerializeOptions struct {
	WorkDir string // Working directory (repo root)
}

// Serialize writes the PR data to files in the working directory.
func Serialize(pr *PullRequest, opts SerializeOptions) error {
	// Group threads by file path
	threadsByFile := make(map[string][]ReviewThread)
	for _, thread := range pr.ReviewThreads {
		threadsByFile[thread.Path] = append(threadsByFile[thread.Path], thread)
	}

	// Process each file
	for path, threads := range threadsByFile {
		if err := serializeFileComments(opts.WorkDir, path, threads); err != nil {
			return fmt.Errorf("serializing %s: %w", path, err)
		}
	}

	// Write PR-STATE.txt
	if err := serializePRState(pr, opts.WorkDir); err != nil {
		return fmt.Errorf("serializing PR state: %w", err)
	}

	return nil
}

// serializeFileComments writes review threads as comments into a source file.
func serializeFileComments(workDir, path string, threads []ReviewThread) error {
	fullPath := filepath.Join(workDir, path)

	// Read original file
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	style := getCommentStyle(path)

	// Sort threads by line number (descending) so we insert from bottom to top
	// This way line numbers don't shift as we insert
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].Line > threads[j].Line
	})

	// Group threads by line for handling multiple threads on same line
	threadsByLine := make(map[int][]ReviewThread)
	for _, thread := range threads {
		threadsByLine[thread.Line] = append(threadsByLine[thread.Line], thread)
	}

	// Process each line's threads
	for line, lineThreads := range threadsByLine {
		// Sort threads on same line by first comment time
		sort.Slice(lineThreads, func(i, j int) bool {
			if len(lineThreads[i].Comments) == 0 || len(lineThreads[j].Comments) == 0 {
				return false
			}
			return lineThreads[i].Comments[0].CreatedAt.Before(lineThreads[j].Comments[0].CreatedAt)
		})

		var commentLines []string
		for threadIdx, thread := range lineThreads {
			// Add thread marker if this is not the first thread on this line
			needsThreadMarker := threadIdx > 0

			for i, comment := range thread.Comments {
				// First comment in non-first thread needs thread marker
				showThread := needsThreadMarker && i == 0

				header := Header{
					Author:    comment.Author.Login,
					Timestamp: comment.CreatedAt,
					NodeID:    comment.ID,
					IsNew:     comment.IsNew,
					IsFile:    thread.SubjectType == SubjectTypeFile,
					IsThread:  showThread,
				}

				// Handle range comments
				if thread.StartLine != nil && *thread.StartLine != thread.Line {
					header.Range = *thread.StartLine - thread.Line // negative
				}

				commentLines = append(commentLines, formatCraftLine(style.linePrefix, formatHeader(header)))

				// Add body lines
				bodyLines := strings.Split(comment.Body, "\n")
				for _, bodyLine := range bodyLines {
					commentLines = append(commentLines, formatCraftLine(style.linePrefix, bodyLine))
				}
			}
		}

		// Insert after the target line (line numbers are 1-based)
		if line >= 1 && line <= len(lines) {
			// Insert commentLines after lines[line-1]
			newLines := make([]string, 0, len(lines)+len(commentLines))
			newLines = append(newLines, lines[:line]...)
			newLines = append(newLines, commentLines...)
			newLines = append(newLines, lines[line:]...)
			lines = newLines
		}
	}

	// Write back
	return os.WriteFile(fullPath, []byte(strings.Join(lines, "\n")), 0644)
}

// serializePRState writes PR-STATE.txt with metadata and issue comments.
func serializePRState(pr *PullRequest, workDir string) error {
	var buf strings.Builder

	// PR metadata header
	metaFields := []string{
		"pr",
		fmt.Sprintf("number %d", pr.Number),
		"id " + formatNodeID(pr.ID),
		"head " + pr.HeadRefOID[:12], // Short hash
	}
	buf.WriteString(headerStart + " " + strings.Join(metaFields, headerFieldSep) + " " + headerStart + "\n")
	buf.WriteString("\n")

	// Files with comments (for deserialization)
	files := make(map[string]bool)
	for _, thread := range pr.ReviewThreads {
		files[thread.Path] = true
	}
	if len(files) > 0 {
		var fileList []string
		for f := range files {
			fileList = append(fileList, f)
		}
		sort.Strings(fileList)
		buf.WriteString("files: " + strings.Join(fileList, ", ") + "\n")
	}
	buf.WriteString("\n")

	// Issue comments
	for _, comment := range pr.IssueComments {
		header := Header{
			Author:    comment.Author.Login,
			Timestamp: comment.CreatedAt,
			NodeID:    comment.ID,
			IsNew:     comment.IsNew,
		}
		buf.WriteString(formatHeader(header) + "\n")

		bodyLines := strings.Split(comment.Body, "\n")
		for _, line := range bodyLines {
			buf.WriteString(line + "\n")
		}
		buf.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(workDir, prStateFile), []byte(buf.String()), 0644)
}

// Deserialize reads PR data from files in the working directory.
func Deserialize(opts SerializeOptions) (*PullRequest, error) {
	pr := &PullRequest{}

	// Read PR-STATE.txt first to get metadata and file list
	statePath := filepath.Join(opts.WorkDir, prStateFile)
	stateContent, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("reading PR state: %w", err)
	}

	files, err := deserializePRState(pr, string(stateContent))
	if err != nil {
		return nil, fmt.Errorf("parsing PR state: %w", err)
	}

	// Read comments from each file
	for _, path := range files {
		threads, err := deserializeFileComments(opts.WorkDir, path)
		if err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", path, err)
		}
		pr.ReviewThreads = append(pr.ReviewThreads, threads...)
	}

	return pr, nil
}

// deserializePRState parses PR-STATE.txt and returns the list of files with comments.
func deserializePRState(pr *PullRequest, content string) ([]string, error) {
	lines := strings.Split(content, "\n")
	var files []string
	var currentComment *IssueComment
	var bodyLines []string

	flushComment := func() {
		if currentComment != nil {
			currentComment.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
			pr.IssueComments = append(pr.IssueComments, *currentComment)
			currentComment = nil
			bodyLines = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for header
		if header, ok := parseHeader(trimmed); ok {
			flushComment()

			// Check if it's the PR metadata header
			if strings.Contains(trimmed, headerFieldSep+"pr"+headerFieldSep) ||
				strings.HasPrefix(trimmed, headerStart+" pr"+headerFieldSep) {
				// Parse PR metadata from header
				pr.ID = header.NodeID
				// Parse additional fields from the raw line
				if match := regexp.MustCompile(`number (\d+)`).FindStringSubmatch(trimmed); match != nil {
					fmt.Sscanf(match[1], "%d", &pr.Number)
				}
				if match := regexp.MustCompile(`head ([a-f0-9]+)`).FindStringSubmatch(trimmed); match != nil {
					pr.HeadRefOID = match[1]
				}
				continue
			}

			// It's a comment header
			currentComment = &IssueComment{
				ID:        header.NodeID,
				Author:    Actor{Login: header.Author},
				CreatedAt: header.Timestamp,
				UpdatedAt: header.Timestamp,
				IsNew:     header.IsNew,
			}
			continue
		}

		// Check for files list
		if strings.HasPrefix(trimmed, "files:") {
			fileStr := strings.TrimPrefix(trimmed, "files:")
			fileStr = strings.TrimSpace(fileStr)
			if fileStr != "" {
				files = strings.Split(fileStr, ", ")
			}
			continue
		}

		// Body line for current comment
		if currentComment != nil {
			bodyLines = append(bodyLines, line)
		}
	}

	flushComment()
	return files, nil
}

// deserializeFileComments parses craft comments from a source file.
func deserializeFileComments(workDir, path string) ([]ReviewThread, error) {
	fullPath := filepath.Join(workDir, path)
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	style := getCommentStyle(path)
	craftLinePrefix := style.linePrefix + " " + craftPrefix + " "

	var threads []ReviewThread
	var currentThread *ReviewThread
	var currentComment *ReviewComment
	var bodyLines []string
	var lastCodeLine int // Line number of the last non-craft line

	flushComment := func() {
		if currentComment != nil {
			currentComment.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
			if currentThread != nil {
				currentThread.Comments = append(currentThread.Comments, *currentComment)
			}
			currentComment = nil
			bodyLines = nil
		}
	}

	flushThread := func() {
		flushComment()
		if currentThread != nil && len(currentThread.Comments) > 0 {
			threads = append(threads, *currentThread)
		}
		currentThread = nil
	}

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Check if this is a craft line
		if strings.HasPrefix(strings.TrimSpace(line), style.linePrefix+" "+craftPrefix) {
			// Extract content after the craft prefix
			content := line
			if idx := strings.Index(content, craftLinePrefix); idx != -1 {
				content = content[idx+len(craftLinePrefix):]
			} else {
				// Handle case with no space after prefix
				content = strings.TrimPrefix(strings.TrimSpace(content), style.linePrefix+" "+craftPrefix)
				content = strings.TrimPrefix(content, " ")
			}

			// Check for header
			if header, ok := parseHeader(content); ok {
				flushComment()

				// Check if we need a new thread
				if currentThread == nil || header.IsThread {
					flushThread()
					currentThread = &ReviewThread{
						Path:        path,
						Line:        lastCodeLine,
						DiffSide:    DiffSideRight, // Default
						SubjectType: SubjectTypeLine,
					}
					if header.IsFile {
						currentThread.SubjectType = SubjectTypeFile
					}
					if header.Range != 0 {
						startLine := lastCodeLine + header.Range
						currentThread.StartLine = &startLine
					}
				}

				currentComment = &ReviewComment{
					ID:        header.NodeID,
					Author:    Actor{Login: header.Author},
					CreatedAt: header.Timestamp,
					UpdatedAt: header.Timestamp,
					IsNew:     header.IsNew,
				}
				continue
			}

			// Body line
			if currentComment != nil {
				bodyLines = append(bodyLines, content)
			}
		} else {
			// Non-craft line - this ends any current thread
			flushThread()
			lastCodeLine = lineNum
		}
	}

	flushThread()

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return threads, nil
}
