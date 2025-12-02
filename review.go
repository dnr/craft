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
	ReviewEvent string // COMMENT, APPROVE, or REQUEST_CHANGES
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
		// Check if this is a new thread (no ID)
		if thread.ID == "" {
			if len(thread.Comments) == 0 {
				continue
			}
			c := thread.Comments[0]
			if !c.IsNew {
				continue
			}
			review.NewThreads = append(review.NewThreads, NewThreadInfo{
				Path:      thread.Path,
				Line:      thread.Line,
				StartLine: thread.StartLine,
				Side:      thread.DiffSide,
				Subject:   thread.SubjectType,
				Body:      c.Body,
			})
		} else {
			// Existing thread - look for new replies
			for _, c := range thread.Comments {
				if !c.IsNew {
					continue
				}
				// Reply to first comment in thread
				if len(thread.Comments) == 0 || thread.Comments[0].ID == "" {
					return nil, fmt.Errorf("cannot find comment to reply to in thread %s:%d", thread.Path, thread.Line)
				}
				review.Replies = append(review.Replies, ReplyInfo{
					ThreadPath:    thread.Path,
					ThreadLine:    thread.Line,
					Body:          c.Body,
					ReplyToNodeID: thread.Comments[0].ID,
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

// Send sends the review to GitHub.
func (r *ReviewToSend) Send(ctx context.Context, client *GitHubClient, prNodeID, headRefOID string) error {
	// Get or create pending review
	fmt.Print("Getting/creating pending review... ")
	reviewID, err := client.getOrCreatePendingReview(ctx, prNodeID, headRefOID)
	if err != nil {
		return fmt.Errorf("getting/creating review: %w", err)
	}
	fmt.Println("done")

	// Add new threads
	for _, t := range r.NewThreads {
		fmt.Printf("Adding thread on %s:%d... ", t.Path, t.Line)
		_, err := client.addReviewThread(ctx, prNodeID, reviewID, t.Path, t.Line, t.StartLine, t.Side, t.Subject, t.Body)
		if err != nil {
			return fmt.Errorf("adding thread: %w", err)
		}
		fmt.Println("done")
	}

	// Add replies
	for _, reply := range r.Replies {
		fmt.Printf("Adding reply in thread %s:%d... ", reply.ThreadPath, reply.ThreadLine)
		_, err := client.addReviewComment(ctx, reviewID, reply.ReplyToNodeID, reply.Body)
		if err != nil {
			return fmt.Errorf("adding reply: %w", err)
		}
		fmt.Println("done")
	}

	// Submit the review
	fmt.Printf("Submitting review (%s)... ", r.ReviewEvent)
	if err := client.submitReview(ctx, reviewID, r.ReviewEvent, r.Body); err != nil {
		return fmt.Errorf("submitting review: %w", err)
	}
	fmt.Println("done")

	return nil
}
