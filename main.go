package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-github/v74/github"
	"github.com/shurcooL/githubv4"
)

const (
	// Comment formatting constants
	CraftMarker       = "❯" // U+276F
	RuleChar          = "─" // U+2500
	TimeFormat        = "2006-01-02 15:04"
	BranchPrefix      = "pr-"
	PRCommentsFile    = "PR-COMMENTS.txt"
	LeadingDashes     = 5
	MaxLineLength     = 100
	CommitMsgTemplate = "craft: embed PR #%d review comments"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "get":
		handleGet(os.Args[2:])
	case "send":
		handleSend(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("craft - GitHub code review tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  craft get [<pr#>]                   Get PR for review")
	fmt.Println("  craft send [options]                Send review comments")
	fmt.Println()
	fmt.Println("Send options:")
	fmt.Println("  --go                               Actually submit the review")
	fmt.Println("  --approve                          Submit as APPROVE review")
	fmt.Println("  --request_changes                  Submit as REQUEST_CHANGES review")
	fmt.Println("  --comment                          Submit as COMMENT review")
	fmt.Println("  (no event flag)                    Submit as pending/draft review")
}

func handleGet(args []string) {
	owner, repo, err := getRepoInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var prNumber int
	if len(args) > 0 {
		prNumber, err = strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid PR number '%s'\n", args[0])
			os.Exit(1)
		}
	} else {
		// Try to extract PR number from current branch name
		currentBranch, err := getCurrentBranch()
		if err == nil && strings.HasPrefix(currentBranch, BranchPrefix) {
			prNumberStr := strings.TrimPrefix(currentBranch, BranchPrefix)
			if extractedPR, err := strconv.Atoi(prNumberStr); err == nil {
				prNumber = extractedPR
				fmt.Printf("Using PR number %d from current branch '%s'\n", prNumber, currentBranch)
			} else {
				fmt.Fprintf(os.Stderr, "Error: PR number required\n")
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: PR number required\n")
			os.Exit(1)
		}
	}

	client := createGitHubClient()
	ctx := context.Background()

	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PR: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found PR #%d: %s\n", prNumber, pr.GetTitle())
	fmt.Printf("Branch: %s\n", pr.GetHead().GetRef())
	fmt.Printf("Base: %s\n", pr.GetBase().GetRef())

	// Check for uncommitted changes
	if hasUncommittedChanges() {
		fmt.Fprintf(os.Stderr, "Error: You have uncommitted changes. Please commit or stash them first.\n")
		os.Exit(1)
	}

	// Create and switch to PR branch
	branchName := fmt.Sprintf("%s%d", BranchPrefix, prNumber)
	headRef := pr.GetHead().GetRef()
	headSHA := pr.GetHead().GetSHA()

	err = checkoutPRBranch(branchName, headRef, headSHA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking out branch: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Switched to branch '%s'\n", branchName)

	// Fetch and embed PR comments
	fmt.Println("Fetching PR comments...")
	err = embedPRComments(client, ctx, owner, repo, prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error embedding comments: %v\n", err)
		os.Exit(1)
	}

	// Auto-commit the embedded comments to avoid uncommitted changes
	err = commitEmbeddedComments(prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to commit embedded comments: %v\n", err)
	}

	fmt.Println("PR ready for review!")
}

func handleSend(args []string) {
	goFlag := false
	var reviewEvent string // "APPROVE", "REQUEST_CHANGES", "COMMENT", or "" (pending)

	for _, arg := range args {
		switch arg {
		case "--go":
			goFlag = true
		case "--approve":
			reviewEvent = "APPROVE"
		case "--request_changes":
			reviewEvent = "REQUEST_CHANGES"
		case "--comment":
			reviewEvent = "COMMENT"
		}
	}

	owner, repo, err := getRepoInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Extract PR number from current branch
	currentBranch, err := getCurrentBranch()
	if err != nil || !strings.HasPrefix(currentBranch, BranchPrefix) {
		fmt.Fprintf(os.Stderr, "Error: not on a PR branch (expected %s<number>)\n", BranchPrefix)
		os.Exit(1)
	}

	prNumberStr := strings.TrimPrefix(currentBranch, BranchPrefix)
	prNumber, err := strconv.Atoi(prNumberStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid PR branch name '%s'\n", currentBranch)
		os.Exit(1)
	}

	// Collect all new comments from all files
	newComments, err := collectNewComments()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting comments: %v\n", err)
		os.Exit(1)
	}

	if len(newComments) == 0 {
		fmt.Println("No new comments to send")
		return
	}

	// Print what would be sent
	fmt.Printf("Found %d new comment(s) to send to PR #%d:\n\n", len(newComments), prNumber)

	for i, comment := range newComments {
		fmt.Printf("%d. %s", i+1, comment.FilePath)
		if comment.Line > 0 {
			fmt.Printf(":%d", comment.Line)
		}
		fmt.Printf("\n")

		// Wrap comment body for console display (80 chars)
		wrappedLines := wrapText(comment.Body, 80, "   ")
		for _, line := range wrappedLines {
			fmt.Printf("   %s\n", line)
		}
		fmt.Printf("\n")
	}

	// Print API call details
	fmt.Printf("API call that would be made:\n")
	fmt.Printf("Repository: %s/%s\n", owner, repo)
	fmt.Printf("PR: #%d\n", prNumber)
	fmt.Printf("POST /repos/%s/%s/pulls/%d/reviews\n", owner, repo, prNumber)

	if reviewEvent != "" {
		fmt.Printf("Review Event: %s\n", reviewEvent)
	} else {
		fmt.Printf("Review Event: (pending - no event, will be draft)\n")
	}

	fmt.Printf("\nReview Comments (%d):\n", len(newComments))
	for i, comment := range newComments {
		if comment.Line > 0 {
			fmt.Printf("  %d. %s:%d", i+1, comment.FilePath, comment.Line)
			if comment.InReplyTo > 0 {
				fmt.Printf(" (reply to comment %d)", comment.InReplyTo)
			}
			fmt.Printf("\n")
		} else {
			fmt.Printf("  %d. PR-level comment\n", i+1)
		}

		// Show body with indentation
		bodyLines := strings.Split(comment.Body, "\n")
		for _, line := range bodyLines {
			fmt.Printf("     %s\n", line)
		}
	}

	if !goFlag {
		fmt.Printf("\nUse --go to actually send these comments\n")
	} else {
		fmt.Printf("\nSubmitting review...\n")
		client := createGitHubClient()
		err = submitReview(client, owner, repo, prNumber, newComments, reviewEvent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error submitting review: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Review submitted successfully!\n")
	}
}

type CommentToSend struct {
	FilePath  string
	Line      int
	Body      string
	InReplyTo int64 // ID of comment this is replying to (0 if not a reply)
	
	// GraphQL metadata for replies
	InReplyToGraphQLID githubv4.ID // GraphQL ID of comment this is replying to
	ThreadID           githubv4.ID // GraphQL thread ID this belongs to
}

func collectNewComments() ([]CommentToSend, error) {
	var comments []CommentToSend

	// Check PR-level comments file
	if content, err := os.ReadFile(PRCommentsFile); err == nil {
		prComments := NewPRComments()
		if err := prComments.Parse(string(content)); err == nil {
			for _, commentList := range prComments.Comments {
				for _, comment := range commentList {
					if comment.IsNew {
						// Unwrap the comment body - join lines but preserve explicit newlines
						body := unwrapCommentBody(comment.Body)
						comments = append(comments, CommentToSend{
							FilePath: "",
							Line:     0,
							Body:     body,
						})
					}
				}
			}
		}
	}

	// Get list of tracked files from git
	files, err := getTrackedFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to get tracked files: %v", err)
	}

	// Check all tracked files for embedded comments
	for _, path := range files {
		// Skip certain files
		if path == PRCommentsFile || strings.HasSuffix(path, ".exe") {
			continue
		}

		// Only process files with known comment syntaxes
		if getLanguageCommentPrefix(path) == "" {
			continue
		}

		content, err := os.ReadFile(path)
		if err != nil {
			continue // Skip files that can't be read
		}

		fileWithComments := NewFileWithComments(path)
		if err := fileWithComments.Parse(string(content)); err != nil {
			continue // Skip files that can't be parsed
		}

		for lineNum, commentList := range fileWithComments.Comments {
			// Find the top-level comment for this line (for reply detection)
			var topLevelComment *ReviewComment
			for _, comment := range commentList {
				if !comment.IsNew && comment.ParentID == 0 {
					// This is a top-level comment from GitHub
					topLevelComment = &comment
					break
				}
			}

			for _, comment := range commentList {
				if comment.IsNew {
					// Unwrap the comment body - join lines but preserve explicit newlines
					body := unwrapCommentBody(comment.Body)

					commentToSend := CommentToSend{
						FilePath: path,
						Line:     lineNum,
						Body:     body,
					}

					// Determine if this should be a reply using GraphQL metadata
					if topLevelComment != nil {
						// Reply to the existing top-level comment
						commentToSend.InReplyTo = topLevelComment.ID // For REST API compatibility
						commentToSend.InReplyToGraphQLID = topLevelComment.GraphQLID
						commentToSend.ThreadID = topLevelComment.ThreadID
						fmt.Printf("DEBUG: Reply detected - ThreadID: %s\n", topLevelComment.ThreadID)
					}

					comments = append(comments, commentToSend)
				}
			}
		}
	}

	return comments, nil
}

func unwrapCommentBody(body string) string {
	lines := strings.Split(body, "\n")
	var result []string
	var currentParagraph []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			// Empty line - end current paragraph if any, add explicit newline
			if len(currentParagraph) > 0 {
				result = append(result, strings.Join(currentParagraph, " "))
				currentParagraph = nil
			}
			result = append(result, "")
		} else {
			// Non-empty line - add to current paragraph
			currentParagraph = append(currentParagraph, trimmed)
		}
	}

	// Add final paragraph if any
	if len(currentParagraph) > 0 {
		result = append(result, strings.Join(currentParagraph, " "))
	}

	return strings.Join(result, "\n")
}

func submitReview(client *github.Client, owner, repo string, prNumber int, comments []CommentToSend, event string) error {
	ctx := context.Background()

	// Get PR details to get the GraphQL node ID and commit SHA
	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR details: %v", err)
	}
	commitSHA := pr.GetHead().GetSHA()

	// Create GraphQL client using same token
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		// Try reading from gh config
		token = getGitHubToken()
		if token == "" {
			return fmt.Errorf("no GitHub token available")
		}
	}

	graphqlClient := githubv4.NewEnterpriseClient("https://api.github.com/graphql", client.Client())

	// Get PR's GraphQL node ID
	var prQuery struct {
		Repository struct {
			PullRequest struct {
				ID githubv4.ID
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	err = graphqlClient.Query(ctx, &prQuery, map[string]interface{}{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"number": githubv4.Int(prNumber),
	})
	if err != nil {
		return fmt.Errorf("failed to get PR GraphQL ID: %v", err)
	}

	pullRequestID := prQuery.Repository.PullRequest.ID


	// Separate PR-level comments from file comments
	var prLevelComments []string
	var fileComments []CommentToSend

	for _, comment := range comments {
		if comment.Line == 0 {
			prLevelComments = append(prLevelComments, comment.Body)
		} else {
			fileComments = append(fileComments, comment)
		}
	}

	reviewBody := ""
	if len(prLevelComments) > 0 {
		reviewBody = strings.Join(prLevelComments, "\n\n")
	}

	// Separate new threads from replies to existing threads
	var newThreads []CommentToSend
	var replyComments []CommentToSend

	for _, comment := range fileComments {
		if comment.ThreadID != "" {
			// This is a reply to an existing thread
			replyComments = append(replyComments, comment)
		} else {
			// This is a new thread
			newThreads = append(newThreads, comment)
		}
	}

	var reviewID githubv4.ID
	newThreadCount := 0
	replyCount := 0

	// Step 1: Create review with new threads (if any)
	if len(newThreads) > 0 || reviewBody != "" {
		var reviewEvent *githubv4.PullRequestReviewEvent
		if event != "" {
			switch event {
			case "APPROVE":
				e := githubv4.PullRequestReviewEventApprove
				reviewEvent = &e
			case "REQUEST_CHANGES":
				e := githubv4.PullRequestReviewEventRequestChanges
				reviewEvent = &e
			case "COMMENT":
				e := githubv4.PullRequestReviewEventComment
				reviewEvent = &e
			}
		}

		// Build threads for new comments
		var threads []*githubv4.DraftPullRequestReviewThread
		for _, comment := range newThreads {
			thread := &githubv4.DraftPullRequestReviewThread{
				Path: githubv4.String(comment.FilePath),
				Line: githubv4.Int(comment.Line),
				Body: githubv4.String(comment.Body),
			}
			threads = append(threads, thread)
		}

		var reviewMutation struct {
			AddPullRequestReview struct {
				PullRequestReview struct {
					ID    githubv4.ID
					State githubv4.PullRequestReviewState
				}
			} `graphql:"addPullRequestReview(input: $input)"`
		}

		input := githubv4.AddPullRequestReviewInput{
			PullRequestID: pullRequestID,
			Threads:       &threads,
			CommitOID:     (*githubv4.GitObjectID)(&commitSHA),
		}

		if reviewBody != "" {
			input.Body = (*githubv4.String)(&reviewBody)
		}
		if reviewEvent != nil {
			input.Event = reviewEvent
		}

		err = graphqlClient.Mutate(ctx, &reviewMutation, input, nil)
		if err != nil {
			return fmt.Errorf("failed to create review: %v", err)
		}

		reviewID = reviewMutation.AddPullRequestReview.PullRequestReview.ID
		newThreadCount = len(newThreads)
		fmt.Printf("Created review %s with %d new threads\n", reviewID, newThreadCount)
	}

	// Step 2: Add replies to existing threads
	for _, comment := range replyComments {
		var replyMutation struct {
			AddPullRequestReviewThreadReply struct {
				Comment struct {
					ID githubv4.ID
				}
			} `graphql:"addPullRequestReviewThreadReply(input: $input)"`
		}

		replyInput := map[string]interface{}{
			"pullRequestReviewThreadId": comment.ThreadID,
			"body":                      githubv4.String(comment.Body),
			"clientMutationId":          githubv4.String(fmt.Sprintf("craft-reply-%d", replyCount+1)),
		}

		// Add pullRequestReviewId if we have one
		if reviewID != "" {
			replyInput["pullRequestReviewId"] = reviewID
		}

		err = graphqlClient.Mutate(ctx, &replyMutation, replyInput, nil)
		if err != nil {
			fmt.Printf("Warning: failed to add reply: %v\n", err)
		} else {
			fmt.Printf("Added reply to thread at %s:%d\n", comment.FilePath, comment.Line)
			replyCount++
		}
	}

	fmt.Printf("GraphQL submission completed!\n")
	fmt.Printf("Review ID: %s\n", reviewID)
	fmt.Printf("Created %d new thread(s) and %d reply(s)\n", newThreadCount, replyCount)

	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
