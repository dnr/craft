package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var clearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove craft comments from source files",
	Long: `Removes all craft-specific comments from source files and deletes PR-STATE.txt.

This is useful after 'craft send --reply-only' to clean up craft comments
while preserving your code edits.

Examples:
  craft clear            Remove craft comments and commit
  craft clear --dry-run  Show what would be changed without modifying files`,
	RunE: runClear,
	Args: cobra.NoArgs,
}

var flagClearDryRun bool

func init() {
	clearCmd.Flags().BoolVar(&flagClearDryRun, "dry-run", false, "Show what would be changed without modifying files")
	rootCmd.AddCommand(clearCmd)
}

func runClear(cmd *cobra.Command, args []string) error {
	vcs, err := DetectVCS(".")
	if err != nil {
		return err
	}

	root := vcs.Root()
	files, err := vcs.ListFiles()
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}

	var cleared int
	for _, path := range files {
		if path == prStateFile {
			continue
		}

		changed, err := clearCraftComments(root, path, flagClearDryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", path, err)
			continue
		}
		if changed {
			cleared++
		}
	}

	// Delete PR-STATE.txt
	prStatePath := filepath.Join(root, prStateFile)
	if _, err := os.Stat(prStatePath); err == nil {
		if flagClearDryRun {
			fmt.Printf("Would delete %s\n", prStateFile)
		} else {
			if err := os.Remove(prStatePath); err != nil {
				return fmt.Errorf("removing %s: %w", prStateFile, err)
			}
			fmt.Printf("Deleted %s\n", prStateFile)
		}
	}

	if cleared == 0 && !flagClearDryRun {
		fmt.Println("No craft comments found.")
		return nil
	}

	fmt.Printf("Cleared craft comments from %d file(s)\n", cleared)

	if !flagClearDryRun {
		fmt.Print("Committing... ")
		if err := vcs.Commit("craft: clear review comments"); err != nil {
			return fmt.Errorf("committing: %w", err)
		}
		fmt.Println("done")
	}

	return nil
}

// clearCraftComments removes all craft comment lines from a file.
// Returns true if the file was modified.
func clearCraftComments(root, path string, dryRun bool) (bool, error) {
	fullPath := filepath.Join(root, path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return false, err
	}

	cleared, changed := clearCraftContent(string(content), path)
	if !changed {
		return false, nil
	}

	if dryRun {
		fmt.Printf("Would clear craft comments from %s\n", path)
		return true, nil
	}

	return true, os.WriteFile(fullPath, []byte(cleared), 0644)
}

// clearCraftContent removes all craft comment lines from file content.
// Returns the cleaned content and whether any changes were made.
func clearCraftContent(content, path string) (string, bool) {
	style := getCommentStyle(path)
	lines := strings.Split(content, "\n")

	var result []string
	inOutdatedSection := false
	changed := false

	for _, line := range lines {
		// Check for outdated comments header
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, outdatedCommentsHeader) {
			inOutdatedSection = true
			changed = true
			continue
		}

		// In the outdated section, everything is craft
		if inOutdatedSection {
			changed = true
			continue
		}

		// Check for craft box characters
		_, _, isCraft := parseCraftLine(line, style.linePrefix)
		if isCraft {
			changed = true
			continue
		}

		result = append(result, line)
	}

	if !changed {
		return content, false
	}

	// Trim trailing blank lines that were before the outdated section
	for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}
	// Ensure file ends with a newline
	result = append(result, "")

	return strings.Join(result, "\n"), true
}
