package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing/fstest"
	"time"

	"rsc.io/markdown"
)

// DirFS wraps a directory path and implements fs.FS.
type DirFS string

func (d DirFS) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(string(d), name))
}

func (d DirFS) Root() string { return string(d) }

const (
	// Box drawing characters for craft comments
	boxThread = "╓" // start of new thread (header line)
	boxReply  = "╟" // reply within thread (header line)
	boxBody   = "║" // body line

	headerStart    = "─────"
	headerFieldSep = " ─ "
	prStateFile    = "PR-STATE.txt"
	defaultWrap    = 80 // Default wrap width for comment text
)

// getIndent returns the leading whitespace of a line.
func getIndent(line string) string {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[:i]
		}
	}
	return line // all whitespace
}

// wrapCommentBody wraps a comment body to fit within the given width,
// accounting for the prefix that will be added to each line.
func wrapCommentBody(body string, prefixLen int) string {
	width := defaultWrap - prefixLen
	if width < 20 {
		width = 20 // minimum reasonable width
	}

	p := markdown.Parser{}
	doc := p.Parse(body)
	wrapped := Wrap(doc, width)
	result := markdown.Format(wrapped)

	// Trim trailing newline that Format adds
	return strings.TrimSuffix(result, "\n")
}

// unwrapCommentBody joins soft-wrapped lines in a comment body.
func unwrapCommentBody(body string) string {
	p := markdown.Parser{}
	doc := p.Parse(body)
	unwrapped := Unwrap(doc)
	result := markdown.Format(unwrapped)

	// Trim trailing newline that Format adds
	return strings.TrimSuffix(result, "\n")
}

// commentStyle defines how comments work for a language.
type commentStyle struct {
	linePrefix string // e.g., "//" or "#"
}

var commentStyles = map[string]commentStyle{
	".go":    {linePrefix: "//"},
	".js":    {linePrefix: "//"},
	".ts":    {linePrefix: "//"},
	".tsx":   {linePrefix: "//"},
	".jsx":   {linePrefix: "//"},
	".java":  {linePrefix: "//"},
	".c":     {linePrefix: "//"},
	".cpp":   {linePrefix: "//"},
	".cc":    {linePrefix: "//"},
	".h":     {linePrefix: "//"},
	".hpp":   {linePrefix: "//"},
	".rs":    {linePrefix: "//"},
	".swift": {linePrefix: "//"},
	".kt":    {linePrefix: "//"},
	".scala": {linePrefix: "//"},
	".py":    {linePrefix: "#"},
	".rb":    {linePrefix: "#"},
	".sh":    {linePrefix: "#"},
	".bash":  {linePrefix: "#"},
	".zsh":   {linePrefix: "#"},
	".pl":    {linePrefix: "#"},
	".pm":    {linePrefix: "#"},
	".r":     {linePrefix: "#"},
	".R":     {linePrefix: "#"},
	".yaml":  {linePrefix: "#"},
	".yml":   {linePrefix: "#"},
	".toml":  {linePrefix: "#"},
	".tf":    {linePrefix: "#"},
	".lua":   {linePrefix: "--"},
	".hs":    {linePrefix: "--"},
	".sql":   {linePrefix: "--"},
	".elm":   {linePrefix: "--"},
	".erl":   {linePrefix: "%"},
	".ex":    {linePrefix: "#"},
	".exs":   {linePrefix: "#"},
	".clj":   {linePrefix: ";;"},
	".lisp":  {linePrefix: ";;"},
	".scm":   {linePrefix: ";;"},
	".vim":   {linePrefix: "\""},
	".el":    {linePrefix: ";;"},
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
// boxChar should be boxThread, boxReply, or boxBody.
// For headers (starting with ─), no space between box char and content: ╓─────
// For body lines, space after box char: ║ text
func formatCraftLine(linePrefix, boxChar, content string) string {
	if strings.HasPrefix(content, "─") {
		return linePrefix + " " + boxChar + content
	}
	return linePrefix + " " + boxChar + " " + content
}

// isCraftLine checks if a line (after trimming) starts with a craft box character.
// Returns the box char and remaining content, or empty string if not a craft line.
func parseCraftLine(line, commentPrefix string) (boxChar, content string, ok bool) {
	line = strings.TrimSpace(line)
	prefix := commentPrefix + " "
	if !strings.HasPrefix(line, prefix) {
		return "", "", false
	}
	line = strings.TrimPrefix(line, prefix)
	// Check for any of the box characters
	for _, box := range []string{boxThread, boxReply, boxBody} {
		if strings.HasPrefix(line, box) {
			content = strings.TrimPrefix(line, box)
			content = strings.TrimPrefix(content, " ") // optional space after box char
			return box, content, true
		}
	}
	return "", "", false
}

// Header represents a parsed comment header.
type Header struct {
	Author     string
	Timestamp  time.Time
	NodeID     string // Full node ID like "PRRC_kwDOPgi5ks6ZBMOo"
	IsNew      bool
	IsFile     bool // file-level comment
	Range      int  // negative number for range comments (e.g., -12 means 12 lines above)
	IsOutdated bool // code has changed since comment was made
	IsResolved bool // thread has been resolved
	OrigLine   int  // original line number (for outdated threads)
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
// Result looks like: ────── @author ─ at 2025-01-15 12:34 ─ prrc xxx
func formatHeader(h Header) string {
	var fields []string

	if h.IsNew {
		fields = append(fields, "new")
	} else {
		if h.Author != "" {
			fields = append(fields, "@"+h.Author)
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

	if h.IsOutdated {
		fields = append(fields, "outdated")
	}

	if h.IsResolved {
		fields = append(fields, "resolved")
	}

	if h.OrigLine != 0 {
		fields = append(fields, fmt.Sprintf("origline %d", h.OrigLine))
	}

	if h.NodeID != "" {
		fields = append(fields, formatNodeID(h.NodeID))
	}

	return headerStart + " " + strings.Join(fields, headerFieldSep)
}

// parseHeader parses a header line into a Header struct.
// Accepts headers starting with ───── (trailing dashes optional for backwards compat).
func parseHeader(line string) (Header, bool) {
	if !strings.HasPrefix(line, headerStart) {
		return Header{}, false
	}

	// Strip leading delimiter and optional trailing delimiter
	content := strings.TrimPrefix(line, headerStart)
	content = strings.TrimSuffix(content, headerStart) // optional, for backwards compat
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
		case field == "outdated":
			h.IsOutdated = true
		case field == "resolved":
			h.IsResolved = true
		case strings.HasPrefix(field, "@"):
			h.Author = strings.TrimPrefix(field, "@")
		case strings.HasPrefix(field, "by "):
			h.Author = strings.TrimPrefix(field, "by ") // backwards compat
		case strings.HasPrefix(field, "at "):
			ts := strings.TrimPrefix(field, "at ")
			if t, err := time.Parse("2006-01-02 15:04", ts); err == nil {
				h.Timestamp = t
			}
		case strings.HasPrefix(field, "range "):
			fmt.Sscanf(field, "range %d", &h.Range)
		case strings.HasPrefix(field, "origline "):
			fmt.Sscanf(field, "origline %d", &h.OrigLine)
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
	// Read original file (may not exist for deleted files)
	content, err := fsReadFile(fsys, path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading file: %w", err)
	}

	style := getCommentStyle(path)

	// Strip existing craft comments to make serialization idempotent
	var lines []string
	if content != nil {
		for _, line := range strings.Split(string(content), "\n") {
			// Check if line contains any craft box character after comment prefix
			_, _, isCraft := parseCraftLine(line, style.linePrefix)
			if !isCraft {
				lines = append(lines, line)
			}
		}
	}

	// Separate threads into valid (line in bounds, RIGHT side) and outdated
	// LEFT side comments are on deleted/old code, so treat as outdated
	var validThreads, outdatedThreads []ReviewThread
	for _, thread := range threads {
		if thread.DiffSide == DiffSideLeft {
			// LEFT side = comment on old/deleted code
			outdatedThreads = append(outdatedThreads, thread)
		} else if thread.Line >= 1 && thread.Line <= len(lines) {
			validThreads = append(validThreads, thread)
		} else {
			outdatedThreads = append(outdatedThreads, thread)
		}
	}

	// Sort valid threads by line number (descending) so we insert from bottom to top
	// This way line numbers don't shift as we insert
	sort.Slice(validThreads, func(i, j int) bool {
		return validThreads[i].Line > validThreads[j].Line
	})

	// Group threads by line for handling multiple threads on same line
	threadsByLine := make(map[int][]ReviewThread)
	for _, thread := range validThreads {
		threadsByLine[thread.Line] = append(threadsByLine[thread.Line], thread)
	}

	// Calculate prefix length for wrapping: "// ║ " = comment + space + box + space
	prefixLen := len(style.linePrefix) + 1 + len(boxBody) + 1

	// Get line numbers and sort in descending order so insertions don't shift earlier lines
	var lineNums []int
	for line := range threadsByLine {
		lineNums = append(lineNums, line)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lineNums)))

	// Process each line's threads (in descending line order)
	for _, line := range lineNums {
		lineThreads := threadsByLine[line]
		// Sort threads on same line by first comment time
		sort.Slice(lineThreads, func(i, j int) bool {
			if len(lineThreads[i].Comments) == 0 || len(lineThreads[j].Comments) == 0 {
				return false
			}
			return lineThreads[i].Comments[0].CreatedAt.Before(lineThreads[j].Comments[0].CreatedAt)
		})

		// Get indentation from the target line
		indent := getIndent(lines[line-1])

		var commentLines []string
		for threadIdx, thread := range lineThreads {
			for i, comment := range thread.Comments {
				// Determine box char: ╓ for first comment or new thread, ╟ for replies
				boxChar := boxReply
				if i == 0 && threadIdx == 0 {
					boxChar = boxThread // first comment of first thread
				} else if i == 0 && threadIdx > 0 {
					boxChar = boxThread // first comment of subsequent thread (new thread)
				}

				header := Header{
					Author:     comment.Author.Login,
					Timestamp:  comment.CreatedAt,
					NodeID:     comment.ID,
					IsNew:      comment.IsNew,
					IsFile:     thread.SubjectType == SubjectTypeFile,
					IsOutdated: thread.IsOutdated,
					IsResolved: thread.IsResolved,
				}

				// Handle range comments
				if thread.StartLine != nil && *thread.StartLine != thread.Line {
					header.Range = *thread.StartLine - thread.Line // negative
				}

				commentLines = append(commentLines, indent+formatCraftLine(style.linePrefix, boxChar, formatHeader(header)))

				// Wrap and add body lines
				wrappedBody := wrapCommentBody(comment.Body, prefixLen+len(indent))
				for _, bodyLine := range strings.Split(wrappedBody, "\n") {
					commentLines = append(commentLines, indent+formatCraftLine(style.linePrefix, boxBody, bodyLine))
				}
			}
		}

		// Insert after the target line (line numbers are 1-based)
		newLines := make([]string, 0, len(lines)+len(commentLines))
		newLines = append(newLines, lines[:line]...)
		newLines = append(newLines, commentLines...)
		newLines = append(newLines, lines[line:]...)
		lines = newLines
	}

	// Append outdated threads at end of file
	if len(outdatedThreads) > 0 {
		// Sort by original line number for consistent ordering
		sort.Slice(outdatedThreads, func(i, j int) bool {
			return outdatedThreads[i].OriginalLine < outdatedThreads[j].OriginalLine
		})

		lines = append(lines, "", style.linePrefix+" ━━━━━━━━━ outdated comments")

		for threadIdx, thread := range outdatedThreads {
			for i, comment := range thread.Comments {
				// ╓ for first comment or new thread, ╟ for replies
				boxChar := boxReply
				if i == 0 {
					boxChar = boxThread // new thread for each outdated thread
				}
				_ = threadIdx // each outdated thread starts fresh

				header := Header{
					Author:     comment.Author.Login,
					Timestamp:  comment.CreatedAt,
					NodeID:     comment.ID,
					IsNew:      comment.IsNew,
					IsFile:     thread.SubjectType == SubjectTypeFile,
					IsOutdated: true,
					IsResolved: thread.IsResolved,
					OrigLine:   thread.OriginalLine,
				}

				lines = append(lines, formatCraftLine(style.linePrefix, boxChar, formatHeader(header)))

				// Wrap and add body lines
				wrappedBody := wrapCommentBody(comment.Body, prefixLen)
				for _, bodyLine := range strings.Split(wrappedBody, "\n") {
					lines = append(lines, formatCraftLine(style.linePrefix, boxBody, bodyLine))
				}
			}
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
		formatNodeID(pr.ID),
		"head " + pr.HeadRefOID,
	}
	if pr.Author.Login != "" {
		metaFields = append(metaFields, "@"+pr.Author.Login)
	}
	buf.WriteString(headerStart + " " + strings.Join(metaFields, headerFieldSep) + "\n")

	// PR description body (informational only, ignored on deserialize)
	if pr.Body != "" {
		buf.WriteString(wrapCommentBody(pr.Body, 0) + "\n")
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

		// Wrap body (no prefix for PR-STATE.txt)
		wrappedBody := wrapCommentBody(comment.Body, 0)
		for _, line := range strings.Split(wrappedBody, "\n") {
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
			if errors.Is(err, syscall.EISDIR) {
				// harmless error caused by submodules
				continue
			}
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
			body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
			// Unwrap soft-wrapped lines to restore original markdown
			currentComment.Body = unwrapCommentBody(body)
			pr.IssueComments = append(pr.IssueComments, *currentComment)
			currentComment = nil
			bodyLines = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for header
		header, isHeader := parseHeader(trimmed)
		if !isHeader {
			// Body line for current comment
			if currentComment != nil {
				bodyLines = append(bodyLines, line)
			}
			continue
		}

		flushComment()

		// Check if it's the PR metadata header
		if strings.Contains(trimmed, headerFieldSep+"pr"+headerFieldSep) ||
			strings.HasPrefix(trimmed, headerStart+" pr"+headerFieldSep) {
			// Parse PR metadata from header
			pr.ID = header.NodeID
			pr.Author.Login = header.Author
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
	}

	flushComment()
	return nil
}

// deserializeFileComments parses craft comments from a source file.
func deserializeFileComments(fsys fs.FS, path string) ([]ReviewThread, error) {
	content, err := fsReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	style := getCommentStyle(path)

	// Skip binary files or files that don't contain any box characters
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, nil
	}
	hasBox := bytes.Contains(content, []byte(boxThread)) ||
		bytes.Contains(content, []byte(boxReply)) ||
		bytes.Contains(content, []byte(boxBody))
	if !hasBox {
		return nil, nil
	}

	var threads []ReviewThread
	var currentThread *ReviewThread
	var currentComment *ReviewComment
	var bodyLines []string
	var lastCodeLine int // Line number of the last non-craft line

	flushComment := func() {
		if currentComment != nil {
			body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
			// Unwrap soft-wrapped lines to restore original markdown
			currentComment.Body = unwrapCommentBody(body)
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

	lines := strings.Split(string(content), "\n")
	sourceLineNum := 0 // line number excluding craft comments
	for _, line := range lines {
		// Check if this is a craft line
		boxChar, craftContent, isCraft := parseCraftLine(line, style.linePrefix)
		if !isCraft {
			// Non-craft line - this ends any current thread
			flushThread()
			sourceLineNum++
			lastCodeLine = sourceLineNum
			continue
		}

		// Check for header (starts with ─────)
		header, isHeader := parseHeader(craftContent)
		if !isHeader {
			// Body line (║)
			if currentComment != nil {
				bodyLines = append(bodyLines, craftContent)
			}
			continue
		}

		// Header line - flush current comment
		flushComment()

		// ╓ = new thread, ╟ = reply within same thread
		if currentThread == nil || boxChar == boxThread {
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
	}

	flushThread()

	return threads, nil
}
