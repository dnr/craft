package main

import (
	"context"
	"fmt"
)

// ReviewToSend contains all the new comments to send in a review.
type ReviewToSend struct {
	NewThreads  []NewThreadInfo
	Replies     []ReplyInfo
	Body        string // PR-level comment (at most one)
	ReviewEvent string // COMMENT, APPROVE, REQUEST_CHANGES, or PENDING (not a real event)
}

type NewThreadInfo struct {
	Path      string
	Line      int
	StartLine *int // Start line for multi-line comments (nil for single line)
	Side      DiffSide
	Subject   SubjectType
	Body      string
}

type ReplyInfo struct {
	ThreadPath    string
	ThreadLine    int
	Body          string
	ReplyToNodeID string
}

// CollectNewComments extracts new comments from a PullRequest into a ReviewToSend.
// Returns an error if there's more than one new PR-level comment.
func CollectNewComments(pr *PullRequest) (*ReviewToSend, error) {
	review := &ReviewToSend{
		ReviewEvent: "COMMENT",
	}

	for _, thread := range pr.ReviewThreads {
		if len(thread.Comments) == 0 {
			continue
		}

		// Check if this is a new thread by looking at the first comment's ID.
		// Thread IDs (PRRT) aren't round-tripped through serialization, but
		// comment IDs (PRRC) are, so we use the first comment's ID to distinguish
		// new threads from existing ones.
		firstComment := thread.Comments[0]
		isNewThread := firstComment.ID == ""

		if isNewThread {
			if !firstComment.IsNew {
				continue
			}
			review.NewThreads = append(review.NewThreads, NewThreadInfo{
				Path:      thread.Path,
				Line:      thread.Line,
				StartLine: thread.StartLine,
				Side:      thread.DiffSide,
				Subject:   thread.SubjectType,
				Body:      firstComment.Body,
			})
		} else {
			// Existing thread - look for new replies
			for _, c := range thread.Comments {
				if !c.IsNew {
					continue
				}
				review.Replies = append(review.Replies, ReplyInfo{
					ThreadPath:    thread.Path,
					ThreadLine:    thread.Line,
					Body:          c.Body,
					ReplyToNodeID: firstComment.ID,
				})
			}
		}
	}

	// Check for new issue comments (PR-level)
	for _, c := range pr.IssueComments {
		if c.IsNew {
			if review.Body != "" {
				return nil, fmt.Errorf("only one new PR-level comment is supported per review")
			}
			review.Body = c.Body
		}
	}

	return review, nil
}

// IsEmpty returns true if there are no comments to send.
func (r *ReviewToSend) IsEmpty() bool {
	return len(r.NewThreads) == 0 && len(r.Replies) == 0 && r.Body == ""
}

// Summary returns a human-readable summary of what will be sent.
func (r *ReviewToSend) Summary() string {
	return fmt.Sprintf("%d new thread(s), %d reply/replies, PR-level comment: %v",
		len(r.NewThreads), len(r.Replies), r.Body != "")
}

// PrintDryRun prints what would be sent without sending.
func (r *ReviewToSend) PrintDryRun() {
	fmt.Println("\n━━━━━ DRY RUN ━━━━━")
	for _, t := range r.NewThreads {
		fmt.Printf("\nNew thread on %s:%d (%s):\n  %s\n", t.Path, t.Line, t.Side, t.Body)
	}
	for _, reply := range r.Replies {
		fmt.Printf("\nReply in thread %s:%d:\n  %s\n", reply.ThreadPath, reply.ThreadLine, reply.Body)
	}
	if r.Body != "" {
		fmt.Printf("\nPR-level comment:\n  %s\n", r.Body)
	}
	fmt.Printf("\nReview event: %s\n", r.ReviewEvent)
}

// ErrPendingReviewExists is returned when there's an existing pending review
// and new threads need to be created.
var ErrPendingReviewExists = fmt.Errorf("pending review exists")

// Send sends the review to GitHub.
// If discardPendingReview is true and there's an existing pending review with new threads
// to add, the existing review will be discarded.
// If ReviewEvent is "PENDING", the review will not be submitted (left in pending state).
func (r *ReviewToSend) Send(ctx context.Context, client *GitHubClient, prNodeID, headRefOID string, discardPendingReview bool) error {
	var reviewID interface{}
	var err error

	// Check for existing pending review
	fmt.Print("Getting/creating pending review... ")
	existingReviewID, hasPending, err := client.getPendingReview(ctx, prNodeID)
	if err != nil {
		return fmt.Errorf("checking for pending review: %w", err)
	}

	if len(r.NewThreads) > 0 {
		// We have new threads - due to a GitHub bug, we must create them atomically
		// with the review, not add them to an existing review.
		if hasPending {
			if !discardPendingReview {
				fmt.Println()
				return fmt.Errorf("%w: you have an existing pending review; use --discard-pending-review to discard it, or submit/discard it in the GitHub UI first", ErrPendingReviewExists)
			}
			// Discard the existing review
			fmt.Print("discarding existing... ")
			if err := client.deletePendingReview(ctx, existingReviewID); err != nil {
				return fmt.Errorf("discarding pending review: %w", err)
			}
		}
		// Create new review with threads
		reviewID, err = client.startReviewWithThreads(ctx, prNodeID, headRefOID, r.NewThreads)
		if err != nil {
			return fmt.Errorf("creating review with threads: %w", err)
		}
	} else {
		// No new threads - just get or create a pending review for replies
		if hasPending {
			reviewID = existingReviewID
		} else {
			reviewID, err = client.startReviewWithThreads(ctx, prNodeID, headRefOID, nil)
			if err != nil {
				return fmt.Errorf("creating review: %w", err)
			}
		}
	}
	fmt.Println("done")

	// Add replies
	for _, reply := range r.Replies {
		fmt.Printf("Adding reply in thread %s:%d... ", reply.ThreadPath, reply.ThreadLine)
		_, err := client.addReviewComment(ctx, reviewID, reply.ReplyToNodeID, reply.Body)
		if err != nil {
			return fmt.Errorf("adding reply: %w", err)
		}
		fmt.Println("done")
	}

	// Submit the review (unless PENDING)
	if r.ReviewEvent != "PENDING" {
		fmt.Printf("Submitting review (%s)... ", r.ReviewEvent)
		if err := client.submitReview(ctx, reviewID, r.ReviewEvent, r.Body); err != nil {
			return fmt.Errorf("submitting review: %w", err)
		}
		fmt.Println("done")
	}

	return nil
}
