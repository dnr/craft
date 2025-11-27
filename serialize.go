package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing/fstest"
	"time"
)

// DirFS wraps a directory path and implements fs.FS.
type DirFS string

func (d DirFS) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(string(d), name))
}

func (d DirFS) Root() string { return string(d) }

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
	FS fs.FS // Filesystem to read/write (use *os.Root or fstest.MapFS)
}

// Serialize writes the PR data to files in the filesystem.
func Serialize(pr *PullRequest, opts SerializeOptions) error {
	// Group threads by file path
	threadsByFile := make(map[string][]ReviewThread)
	for _, thread := range pr.ReviewThreads {
		threadsByFile[thread.Path] = append(threadsByFile[thread.Path], thread)
	}

	// Process each file
	for path, threads := range threadsByFile {
		if err := serializeFileComments(opts.FS, path, threads); err != nil {
			return fmt.Errorf("serializing %s: %w", path, err)
		}
	}

	// Write PR-STATE.txt
	if err := serializePRState(pr, opts.FS); err != nil {
		return fmt.Errorf("serializing PR state: %w", err)
	}

	return nil
}

// fsReadFile reads a file from the filesystem.
func fsReadFile(fsys fs.FS, name string) ([]byte, error) {
	return fs.ReadFile(fsys, name)
}

// fsWriteFile writes a file to the filesystem.
func fsWriteFile(fsys fs.FS, name string, data []byte) error {
	switch f := fsys.(type) {
	case fstest.MapFS:
		f[name] = &fstest.MapFile{Data: data}
		return nil
	case DirFS:
		return os.WriteFile(filepath.Join(string(f), name), data, 0644)
	default:
		return fmt.Errorf("unsupported filesystem type %T for writing", fsys)
	}
}

// serializeFileComments writes review threads as comments into a source file.
func serializeFileComments(fsys fs.FS, path string, threads []ReviewThread) error {
	// Read original file
	content, err := fsReadFile(fsys, path)
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
	return fsWriteFile(fsys, path, []byte(strings.Join(lines, "\n")))
}

// serializePRState writes PR-STATE.txt with metadata and issue comments.
func serializePRState(pr *PullRequest, fsys fs.FS) error {
	var buf strings.Builder

	// PR metadata header
	metaFields := []string{
		"pr",
		fmt.Sprintf("number %d", pr.Number),
		"id " + formatNodeID(pr.ID),
		"head " + pr.HeadRefOID,
	}
	buf.WriteString(headerStart + " " + strings.Join(metaFields, headerFieldSep) + " " + headerStart + "\n")
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

	return fsWriteFile(fsys, prStateFile, []byte(buf.String()))
}

// Deserialize reads PR data from files in the filesystem.
func Deserialize(opts SerializeOptions) (*PullRequest, error) {
	pr := &PullRequest{}

	// Read PR-STATE.txt first to get metadata
	stateContent, err := fsReadFile(opts.FS, prStateFile)
	if err != nil {
		return nil, fmt.Errorf("reading PR state: %w", err)
	}

	if err := deserializePRState(pr, string(stateContent)); err != nil {
		return nil, fmt.Errorf("parsing PR state: %w", err)
	}

	// Get list of files
	files, err := fsListFiles(opts.FS)
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}

	// Read comments from each file
	for _, path := range files {
		threads, err := deserializeFileComments(opts.FS, path)
		if err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", path, err)
		}
		pr.ReviewThreads = append(pr.ReviewThreads, threads...)
	}

	return pr, nil
}

// fsListFiles returns all files to scan for comments.
func fsListFiles(fsys fs.FS) ([]string, error) {
	switch f := fsys.(type) {
	case fstest.MapFS:
		var files []string
		for name := range f {
			if name != prStateFile {
				files = append(files, name)
			}
		}
		sort.Strings(files)
		return files, nil
	case DirFS:
		cmd := exec.Command("git", "ls-files")
		cmd.Dir = string(f)
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		var files []string
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" {
				files = append(files, line)
			}
		}
		return files, nil
	default:
		return nil, fmt.Errorf("unsupported filesystem type %T for listing", fsys)
	}
}

// deserializePRState parses PR-STATE.txt into the PullRequest.
func deserializePRState(pr *PullRequest, content string) error {
	lines := strings.Split(content, "\n")
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

		// Body line for current comment
		if currentComment != nil {
			bodyLines = append(bodyLines, line)
		}
	}

	flushComment()
	return nil
}

// deserializeFileComments parses craft comments from a source file.
func deserializeFileComments(fsys fs.FS, path string) ([]ReviewThread, error) {
	file, err := fsys.Open(path)
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
