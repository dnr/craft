package main

import "time"

// Actor represents a GitHub user or bot.
type Actor struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	URL       string `json:"url,omitempty"`
}

// ReviewState represents the state of a pull request review.
type ReviewState string

const (
	ReviewStatePending          ReviewState = "PENDING"
	ReviewStateCommented        ReviewState = "COMMENTED"
	ReviewStateApproved         ReviewState = "APPROVED"
	ReviewStateChangesRequested ReviewState = "CHANGES_REQUESTED"
)

// DiffSide indicates which side of the diff a comment is on.
type DiffSide string

const (
	DiffSideLeft  DiffSide = "LEFT"
	DiffSideRight DiffSide = "RIGHT"
)

// SubjectType indicates whether a comment is on a line or file.
type SubjectType string

const (
	SubjectTypeLine SubjectType = "LINE"
	SubjectTypeFile SubjectType = "FILE"
)

// ReviewComment is a comment within a review thread.
type ReviewComment struct {
	ID         string    `json:"id"`         // GraphQL node ID
	DatabaseID int64     `json:"databaseId"` // Numeric ID
	Author     Actor     `json:"author"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	ReplyToID  *string   `json:"replyToId,omitempty"` // Parent comment ID (for replies within thread)

	// For tracking local changes
	IsNew      bool `json:"isNew,omitempty"`      // Created locally, not yet pushed
	IsModified bool `json:"isModified,omitempty"` // Edited locally
}

// ReviewThread is a thread of comments on a specific code location.
type ReviewThread struct {
	ID                string          `json:"id"` // GraphQL node ID
	Path              string          `json:"path"`
	DiffSide          DiffSide        `json:"diffSide"`
	Line              int             `json:"line"`              // End line (or single line)
	StartLine         *int            `json:"startLine"`         // Start line for ranges (nil if single line)
	OriginalLine      int             `json:"originalLine"`      // Original line (before PR changes)
	OriginalStartLine *int            `json:"originalStartLine"` // Original start line for ranges
	IsOutdated        bool            `json:"isOutdated"`
	IsResolved        bool            `json:"isResolved"`
	SubjectType       SubjectType     `json:"subjectType"`
	Comments          []ReviewComment `json:"comments"`
}

// IssueComment is a general PR comment (not attached to code).
type IssueComment struct {
	ID         string    `json:"id"`
	DatabaseID int64     `json:"databaseId"`
	Author     Actor     `json:"author"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`

	IsNew      bool `json:"isNew,omitempty"`
	IsModified bool `json:"isModified,omitempty"`
}

// Review is a formal review submission.
type Review struct {
	ID          string      `json:"id"`
	DatabaseID  int64       `json:"databaseId"`
	Author      Actor       `json:"author"`
	State       ReviewState `json:"state"`
	Body        string      `json:"body"`
	SubmittedAt *time.Time  `json:"submittedAt,omitempty"` // nil if pending
	CreatedAt   time.Time   `json:"createdAt"`
}

// PullRequest represents the complete PR state.
type PullRequest struct {
	// Identity
	ID     string `json:"id"` // GraphQL node ID
	Number int    `json:"number"`

	// Metadata
	Title   string `json:"title"`
	Body    string `json:"body"`
	Author  Actor  `json:"author"`
	State   string `json:"state"` // OPEN, CLOSED, MERGED
	IsDraft bool   `json:"isDraft"`

	// Branch info
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	BaseRefOID  string `json:"baseRefOid"`
	HeadRefOID  string `json:"headRefOid"`

	// Review data - the core of what we sync
	ReviewThreads []ReviewThread `json:"reviewThreads"`
	IssueComments []IssueComment `json:"issueComments"`
	Reviews       []Review       `json:"reviews"`

	// Sync metadata
	LastFetchedAt time.Time `json:"lastFetchedAt"`
}
