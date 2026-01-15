package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var suggestCmd = &cobra.Command{
	Use:   "suggest",
	Short: "Convert code edits to suggestion comments",
	Long: `Converts direct code edits into craft suggestion comments.

This command compares your current changes against the PR head commit
and converts code modifications into GitHub suggestion comments.

The workflow is:
1. Make direct edits to the code (as if you were the author)
2. Run 'craft suggest' to convert those edits to suggestion comments
3. Review the generated suggestions
4. Run 'craft send' to post the suggestions to GitHub

Changes are classified as:
- Code modifications: suggestion blocks
- Added code comments: regular craft comments
- Pure code additions: skipped (warning shown)
- Lines with craft box chars: skipped (already comments)

Examples:
  craft suggest            Convert edits and commit
  craft suggest --dry-run  Show what would be done without changing files`,
	RunE: runSuggest,
	Args: cobra.NoArgs,
}

var (
	flagSuggestDryRun bool
)

func init() {
	suggestCmd.Flags().BoolVar(&flagSuggestDryRun, "dry-run", false, "Show what would be done without modifying files")
	rootCmd.AddCommand(suggestCmd)
}

// Hunk represents a parsed diff hunk.
type Hunk struct {
	OldStart, OldCount int      // Line range in old file
	NewStart, NewCount int      // Line range in new file
	OldLines           []string // Lines removed (without - prefix)
	NewLines           []string // Lines added (without + prefix)
	Context            []string // Context lines (for reference)
}

// HunkClassification describes what to do with a hunk.
type HunkClassification int

const (
	HunkSkip          HunkClassification = iota // Already craft comment, skip
	HunkSuggestion                              // Code change → suggestion
	HunkCraftComment                            // Added code comment → craft comment
	HunkWarnPureAdd                             // Pure addition, warn and skip
)

func runSuggest(cmd *cobra.Command, args []string) error {
	// Detect VCS
	vcs, err := DetectVCS(".")
	if err != nil {
		return err
	}
	fmt.Printf("Using %s repository at %s\n", vcs.Name(), vcs.Root())

	// Read PR state to get head commit
	opts := SerializeOptions{FS: DirFS(vcs.Root())}
	pr, err := Deserialize(opts)
	if err != nil {
		return fmt.Errorf("reading PR state: %w", err)
	}

	if pr.HeadRefOID == "" {
		return fmt.Errorf("no head commit in PR-STATE.txt, run 'craft get' first")
	}
	fmt.Printf("PR head: %s\n", pr.HeadRefOID[:12])

	// Get list of modified files (comparing PR head to current working tree)
	files, err := getModifiedFiles(vcs, pr.HeadRefOID)
	if err != nil {
		return fmt.Errorf("getting modified files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No modified files found.")
		return nil
	}
	fmt.Printf("Modified files: %d\n", len(files))

	// Process each file
	var stats struct {
		suggestions   int
		craftComments int
		warnings      int
	}

	root := vcs.Root()

	for _, path := range files {
		// Skip PR-STATE.txt
		if path == prStateFile {
			continue
		}

		result, err := processFileForSuggestions(vcs, root, pr.HeadRefOID, path, flagSuggestDryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", path, err)
			continue
		}

		stats.suggestions += result.suggestions
		stats.craftComments += result.craftComments
		stats.warnings += result.warnings
	}

	// Summary
	fmt.Printf("\nResults:\n")
	fmt.Printf("  %d suggestions created\n", stats.suggestions)
	fmt.Printf("  %d craft comments created\n", stats.craftComments)
	if stats.warnings > 0 {
		fmt.Printf("  %d warnings (pure additions skipped)\n", stats.warnings)
	}

	// Commit if not dry-run
	if !flagSuggestDryRun && (stats.suggestions > 0 || stats.craftComments > 0) {
		fmt.Print("\nCommitting changes... ")
		commitMsg := fmt.Sprintf("craft: convert %d edits to suggestions", stats.suggestions+stats.craftComments)
		if err := vcs.Commit(commitMsg); err != nil {
			return fmt.Errorf("committing: %w", err)
		}
		fmt.Println("done")
	}

	return nil
}

type processResult struct {
	suggestions   int
	craftComments int
	warnings      int
}

func processFileForSuggestions(vcs VCS, root, headCommit, path string, dryRun bool) (processResult, error) {
	var result processResult

	// Get the diff for this file
	hunks, err := getFileHunks(vcs, headCommit, path)
	if err != nil {
		return result, err
	}

	if len(hunks) == 0 {
		return result, nil
	}

	// Get original file content from head commit
	originalContent, err := getFileAtCommit(vcs, headCommit, path)
	if err != nil {
		// File might not exist at head commit (newly added file)
		// All changes would be pure additions, skip with warning
		fmt.Fprintf(os.Stderr, "Warning: %s: file not in PR head, skipping (new file?)\n", path)
		return result, nil
	}

	originalLines := strings.Split(originalContent, "\n")
	style := getCommentStyle(path)

	// Classify each hunk
	type classifiedHunk struct {
		hunk           Hunk
		classification HunkClassification
	}
	var classified []classifiedHunk

	for _, hunk := range hunks {
		class := classifyHunk(hunk, style)
		classified = append(classified, classifiedHunk{hunk, class})

		switch class {
		case HunkSuggestion:
			result.suggestions++
		case HunkCraftComment:
			result.craftComments++
		case HunkWarnPureAdd:
			result.warnings++
			fmt.Fprintf(os.Stderr, "Warning: %s:%d: pure code addition, skipping\n", path, hunk.NewStart)
		}
	}

	if result.suggestions == 0 && result.craftComments == 0 {
		return result, nil
	}

	// Build new file content: original code + craft comments/suggestions
	// Process hunks from bottom to top so line numbers stay valid
	sort.Slice(classified, func(i, j int) bool {
		return classified[i].hunk.OldStart > classified[j].hunk.OldStart
	})

	resultLines := make([]string, len(originalLines))
	copy(resultLines, originalLines)

	for _, ch := range classified {
		if ch.classification == HunkSkip || ch.classification == HunkWarnPureAdd {
			continue
		}

		hunk := ch.hunk

		// Get indent from the first old line (or first new line if pure add)
		indent := ""
		if len(hunk.OldLines) > 0 {
			indent = getIndent(hunk.OldLines[0])
		} else if len(hunk.NewLines) > 0 {
			indent = getIndent(hunk.NewLines[0])
		}

		var commentLines []string

		switch ch.classification {
		case HunkSuggestion:
			commentLines = buildSuggestionComment(style, indent, hunk)
		case HunkCraftComment:
			commentLines = buildCraftCommentFromCodeComments(style, indent, hunk)
		}

		// Insert after the hunk's old lines
		// OldStart is 1-based, and we want to insert after the last old line
		insertPos := hunk.OldStart + hunk.OldCount - 1
		if insertPos < 0 {
			insertPos = 0
		}
		if insertPos > len(resultLines) {
			insertPos = len(resultLines)
		}

		newResultLines := make([]string, 0, len(resultLines)+len(commentLines))
		newResultLines = append(newResultLines, resultLines[:insertPos]...)
		newResultLines = append(newResultLines, commentLines...)
		newResultLines = append(newResultLines, resultLines[insertPos:]...)
		resultLines = newResultLines
	}

	// Write or show the result
	if dryRun {
		fmt.Printf("\n--- %s (dry-run) ---\n", path)
		for i, line := range resultLines {
			fmt.Printf("%4d: %s\n", i+1, line)
		}
	} else {
		content := strings.Join(resultLines, "\n")
		fullPath := filepath.Join(root, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return result, fmt.Errorf("writing file: %w", err)
		}
		fmt.Printf("  %s: %d suggestions, %d comments\n", path, result.suggestions, result.craftComments)
	}

	return result, nil
}

// getModifiedFiles returns files modified between commit and working tree.
func getModifiedFiles(vcs VCS, commit string) ([]string, error) {
	switch v := vcs.(type) {
	case *GitRepo:
		out, err := v.run("diff", "--name-only", commit, "HEAD")
		if err != nil {
			return nil, err
		}
		if out == "" {
			return nil, nil
		}
		return strings.Split(out, "\n"), nil
	case *JJRepo:
		out, err := v.run("diff", "--summary", "--from", commit, "--to", "@")
		if err != nil {
			return nil, err
		}
		if out == "" {
			return nil, nil
		}
		// jj diff --summary format: "M path" or "A path" etc.
		var files []string
		for _, line := range strings.Split(out, "\n") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				files = append(files, parts[1])
			}
		}
		return files, nil
	default:
		return nil, fmt.Errorf("unsupported VCS type")
	}
}

// getFileHunks returns parsed diff hunks for a file.
func getFileHunks(vcs VCS, commit, path string) ([]Hunk, error) {
	var diffOutput string
	var err error

	switch v := vcs.(type) {
	case *GitRepo:
		diffOutput, err = v.run("diff", "-U0", commit, "HEAD", "--", path)
	case *JJRepo:
		diffOutput, err = v.run("diff", "--git", "--context=0", "--from", commit, "--to", "@", path)
	default:
		return nil, fmt.Errorf("unsupported VCS type")
	}

	if err != nil {
		return nil, err
	}

	return parseUnifiedDiff(diffOutput), nil
}

// getFileAtCommit returns file content at a specific commit.
func getFileAtCommit(vcs VCS, commit, path string) (string, error) {
	switch v := vcs.(type) {
	case *GitRepo:
		return v.run("show", commit+":"+path)
	case *JJRepo:
		// jj doesn't have a direct "show file at commit" command
		// Use git show through the colocated repo
		out, err := v.run("file", "show", "-r", commit, path)
		if err != nil {
			return "", err
		}
		return out, nil
	default:
		return "", fmt.Errorf("unsupported VCS type")
	}
}

// parseUnifiedDiff parses unified diff output into hunks.
func parseUnifiedDiff(diff string) []Hunk {
	var hunks []Hunk

	// Regex to match hunk headers: @@ -oldStart,oldCount +newStart,newCount @@
	hunkHeaderRe := regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

	lines := strings.Split(diff, "\n")
	var currentHunk *Hunk

	for _, line := range lines {
		if matches := hunkHeaderRe.FindStringSubmatch(line); matches != nil {
			// Save previous hunk
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
			}

			// Parse hunk header
			oldStart := 0
			oldCount := 1 // default if not specified
			newStart := 0
			newCount := 1

			fmt.Sscanf(matches[1], "%d", &oldStart)
			if matches[2] != "" {
				fmt.Sscanf(matches[2], "%d", &oldCount)
			}
			fmt.Sscanf(matches[3], "%d", &newStart)
			if matches[4] != "" {
				fmt.Sscanf(matches[4], "%d", &newCount)
			}

			currentHunk = &Hunk{
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
			}
			continue
		}

		if currentHunk == nil {
			continue
		}

		if strings.HasPrefix(line, "-") {
			currentHunk.OldLines = append(currentHunk.OldLines, strings.TrimPrefix(line, "-"))
		} else if strings.HasPrefix(line, "+") {
			currentHunk.NewLines = append(currentHunk.NewLines, strings.TrimPrefix(line, "+"))
		} else if strings.HasPrefix(line, " ") {
			currentHunk.Context = append(currentHunk.Context, strings.TrimPrefix(line, " "))
		}
	}

	// Don't forget the last hunk
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	return hunks
}

// classifyHunk determines what to do with a hunk.
func classifyHunk(hunk Hunk, style commentStyle) HunkClassification {
	// Filter out craft comment lines from new lines
	var filteredNewLines []string
	for _, line := range hunk.NewLines {
		if !isCraftCommentLine(line) {
			filteredNewLines = append(filteredNewLines, line)
		}
	}

	// If all new lines were craft comments, skip this hunk
	if len(filteredNewLines) == 0 && len(hunk.OldLines) == 0 {
		return HunkSkip
	}

	// If there are deletions, this is a code change → suggestion
	if len(hunk.OldLines) > 0 {
		return HunkSuggestion
	}

	// Pure additions - check if they're all code comments
	allCodeComments := true
	for _, line := range filteredNewLines {
		if !isCodeCommentLine(line, style) {
			allCodeComments = false
			break
		}
	}

	if allCodeComments && len(filteredNewLines) > 0 {
		return HunkCraftComment
	}

	// Pure code addition - warn and skip
	return HunkWarnPureAdd
}

// isCraftCommentLine checks if a line contains craft box characters.
func isCraftCommentLine(line string) bool {
	return strings.Contains(line, boxThread) ||
		strings.Contains(line, boxReply) ||
		strings.Contains(line, boxBody)
}

// isCodeCommentLine checks if a line is a code comment (starts with comment prefix).
func isCodeCommentLine(line string, style commentStyle) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, style.linePrefix)
}

// buildSuggestionComment creates a suggestion comment block.
func buildSuggestionComment(style commentStyle, indent string, hunk Hunk) []string {
	var lines []string

	// Header - use OldCount from hunk header for accurate range
	rangeField := ""
	if hunk.OldCount > 1 {
		rangeField = fmt.Sprintf(" %s range %d", headerFieldSep, -(hunk.OldCount - 1))
	}
	header := indent + formatCraftLine(style.linePrefix, boxThread, headerStart+" new"+rangeField)
	lines = append(lines, header)

	// ```suggestion
	lines = append(lines, indent+formatCraftLine(style.linePrefix, boxBody, "```suggestion"))

	// New lines (the suggested replacement)
	for _, newLine := range hunk.NewLines {
		// Skip craft comment lines in the suggestion
		if isCraftCommentLine(newLine) {
			continue
		}
		lines = append(lines, indent+formatCraftLine(style.linePrefix, boxBody, newLine))
	}

	// ```
	lines = append(lines, indent+formatCraftLine(style.linePrefix, boxBody, "```"))

	return lines
}

// buildCraftCommentFromCodeComments converts code comments to craft comments.
func buildCraftCommentFromCodeComments(style commentStyle, indent string, hunk Hunk) []string {
	var lines []string

	// Header
	header := indent + formatCraftLine(style.linePrefix, boxThread, headerStart+" new")
	lines = append(lines, header)

	// Extract comment text from code comment lines
	for _, newLine := range hunk.NewLines {
		if isCraftCommentLine(newLine) {
			continue
		}
		// Strip the code comment prefix to get just the text
		trimmed := strings.TrimSpace(newLine)
		text := strings.TrimPrefix(trimmed, style.linePrefix)
		text = strings.TrimSpace(text)
		lines = append(lines, indent+formatCraftLine(style.linePrefix, boxBody, text))
	}

	return lines
}
