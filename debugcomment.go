package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var debugCommentCmd = &cobra.Command{
	Use:   "debugcomment",
	Short: "Add a comment to a PR JSON file",
	Long: `Add a new comment or reply to an existing comment thread.

For a new comment on a file/line (creates a new thread):
  craft debugcomment --input pr.json --output pr-new.json \
    --file path/to/file.go --line 42 --side RIGHT --body "comment text"

For a reply to an existing comment:
  craft debugcomment --input pr.json --output pr-new.json \
    --reply-to 12345678 --body "reply text"`,
	RunE: runDebugComment,
}

var (
	flagInput   string
	flagOutput  string
	flagFile    string
	flagLine    int
	flagSide    string
	flagBody    string
	flagReplyTo int64
)

func init() {
	debugCommentCmd.Flags().StringVar(&flagInput, "input", "", "Input JSON file from debugfetch")
	debugCommentCmd.Flags().StringVar(&flagOutput, "output", "", "Output JSON file")
	debugCommentCmd.Flags().StringVar(&flagFile, "file", "", "File path for new comment")
	debugCommentCmd.Flags().IntVar(&flagLine, "line", 0, "Line number for new comment")
	debugCommentCmd.Flags().StringVar(&flagSide, "side", "RIGHT", "Diff side (LEFT or RIGHT)")
	debugCommentCmd.Flags().StringVar(&flagBody, "body", "", "Comment body text")
	debugCommentCmd.Flags().Int64Var(&flagReplyTo, "reply-to", 0, "Database ID of comment to reply to")

	debugCommentCmd.MarkFlagRequired("input")
	debugCommentCmd.MarkFlagRequired("output")
	debugCommentCmd.MarkFlagRequired("body")
}

func runDebugComment(cmd *cobra.Command, args []string) error {
	// Load input JSON
	data, err := os.ReadFile(flagInput)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}

	var pr PullRequest
	if err := json.Unmarshal(data, &pr); err != nil {
		return fmt.Errorf("parsing input JSON: %w", err)
	}

	// Create the new comment
	now := time.Now()
	newComment := ReviewComment{
		// No ID or DatabaseID - these get assigned by GitHub
		Body:      flagBody,
		CreatedAt: now,
		UpdatedAt: now,
		IsNew:     true,
		// Author will be filled in by GitHub when created
	}

	if flagReplyTo != 0 {
		// Reply to existing comment - find the thread containing it
		found := false
		for i := range pr.ReviewThreads {
			thread := &pr.ReviewThreads[i]
			for _, c := range thread.Comments {
				if c.DatabaseID == flagReplyTo {
					rid := fmt.Sprintf("%d", flagReplyTo)
					newComment.ReplyToID = &rid
					thread.Comments = append(thread.Comments, newComment)
					found = true
					fmt.Printf("Added reply to comment %d in thread on %s:%d\n",
						flagReplyTo, thread.Path, thread.Line)
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("comment with database ID %d not found", flagReplyTo)
		}
	} else {
		// New comment - need file and line, always creates a new thread
		if flagFile == "" {
			return fmt.Errorf("--file is required for new comments (use --reply-to for replies)")
		}
		if flagLine == 0 {
			return fmt.Errorf("--line is required for new comments")
		}

		side := DiffSide(flagSide)
		if side != DiffSideLeft && side != DiffSideRight {
			return fmt.Errorf("--side must be LEFT or RIGHT, got %q", flagSide)
		}

		// Create new thread
		newThread := ReviewThread{
			// No ID - assigned by GitHub
			Path:        flagFile,
			DiffSide:    side,
			Line:        flagLine,
			SubjectType: SubjectTypeLine,
			Comments:    []ReviewComment{newComment},
		}
		pr.ReviewThreads = append(pr.ReviewThreads, newThread)
		fmt.Printf("Created new thread on %s:%d\n", flagFile, flagLine)
	}

	// Write output JSON
	outData, err := json.MarshalIndent(&pr, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling output JSON: %w", err)
	}

	if err := os.WriteFile(flagOutput, outData, 0644); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	fmt.Printf("Wrote %s\n", flagOutput)
	return nil
}
