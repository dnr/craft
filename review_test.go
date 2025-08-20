package main

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v74/github"
)

func TestFileWithComments_ParseAndSerialize_SourceFile(t *testing.T) {
	// Test with shorthand syntax and existing header format
	content := `package main

func hello() {
	fmt.Println("Hello")
	//++ This is a new comment I want to add on Hello
	return
}
// ❯ ───── by reviewer ─ date 2024-01-15 14:30 ─────
// ❯ This is a comment about the whole function.
`

	f := NewFileWithComments("test.go")
	err := f.Parse(content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check that source lines were extracted correctly
	expectedLines := []string{
		"package main",
		"",
		"func hello() {",
		"\tfmt.Println(\"Hello\")",
		"\treturn",
		"}",
	}
	if len(f.Lines) != len(expectedLines) {
		t.Fatalf("Expected %d lines, got %d", len(expectedLines), len(f.Lines))
	}
	for i, expected := range expectedLines {
		if f.Lines[i] != expected {
			t.Errorf("Line %d: expected %q, got %q", i, expected, f.Lines[i])
		}
	}

	// Check that comments were parsed correctly
	// New comment should be on line 4 (after "fmt.Println(...)")
	if len(f.Comments[4]) != 1 {
		t.Errorf("Expected 1 comment on line 4, got %d", len(f.Comments[4]))
		return
	}

	// Find the new comment
	var foundNew bool
	for _, comment := range f.Comments[4] {
		if comment.IsNew && strings.Contains(comment.Body, "This is a new comment I want to add on Hello") {
			foundNew = true
			break
		}
	}
	if !foundNew {
		t.Error("Expected to find new comment on line 4")
	}

	// Reviewer comment should be on line 6 (after the "}")
	if len(f.Comments[6]) < 1 {
		t.Errorf("Expected at least 1 comment on line 6, got %d", len(f.Comments[6]))
		return
	}

	var foundReviewer bool
	for _, comment := range f.Comments[6] {
		if !comment.IsNew && comment.Author == "reviewer" {
			foundReviewer = true
			break
		}
	}
	if !foundReviewer {
		t.Error("Expected to find reviewer comment on line 6")
	}

	// Test serialization
	serialized := f.Serialize()
	if !strings.Contains(serialized, "package main") {
		t.Error("Serialized content should contain source code")
	}
	if !strings.Contains(serialized, CraftMarker) {
		t.Error("Serialized content should contain comment marker")
	}
	if !strings.Contains(serialized, "new") {
		t.Errorf("Serialized output should contain 'new' field, got:\n%s", serialized)
	}
}

func TestFileWithComments_ParseAndSerialize_PRComments(t *testing.T) {
	content := `─────── by reviewer ─ date 2024-01-15 14:30 ─────
This is a PR-level comment with multiple paragraphs.

It has line breaks and should be preserved properly.

─────── by alice ─ date 2024-01-15 10:00 ─────
Another comment from a different user.`

	f := NewPRComments()
	err := f.Parse(content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// PR comments should have no source lines
	if len(f.Lines) != 0 {
		t.Errorf("PR comments should have no source lines, got %d", len(f.Lines))
	}

	// All comments should be at line 0
	if len(f.Comments[0]) != 2 {
		t.Errorf("Expected 2 comments at line 0, got %d", len(f.Comments[0]))
	}

	// Check first comment
	comment1 := f.Comments[0][0]
	if comment1.Author != "reviewer" || comment1.IsNew {
		t.Errorf("First comment: expected author=reviewer, IsNew=false, got author=%s, IsNew=%v",
			comment1.Author, comment1.IsNew)
	}
	if !strings.Contains(comment1.Body, "multiple paragraphs") {
		t.Error("First comment body should contain expected text")
	}

	// Check second comment
	comment2 := f.Comments[0][1]
	if comment2.Author != "alice" || comment2.IsNew {
		t.Errorf("Second comment: expected author=alice, IsNew=false, got author=%s, IsNew=%v",
			comment2.Author, comment2.IsNew)
	}

	// Test serialization
	serialized := f.Serialize()
	if strings.Contains(serialized, CraftMarker) {
		t.Error("PR comments should not contain comment markers")
	}
	if !strings.Contains(serialized, RuleChar) {
		t.Error("PR comments should contain rule characters")
	}
	// Note: This test doesn't have any new comments, so no 'new' field expected
}

func TestFileWithComments_SyncWithGitHubComments(t *testing.T) {
	f := NewFileWithComments("test.go")
	f.Lines = []string{"package main", "func test() {}"}

	// Add a new comment that should be preserved
	f.Comments[2] = []ReviewComment{
		{IsNew: true, Body: "My new comment", Author: ""},
	}

	// Create mock GitHub comments
	createdAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)
	ghComments := []*github.PullRequestComment{
		{
			ID:        github.Int64(123),
			Line:      github.Int(2),
			User:      &github.User{Login: github.String("reviewer")},
			Body:      github.String("This function needs documentation"),
			CreatedAt: &github.Timestamp{Time: createdAt},
		},
	}

	f.SyncWithGitHubComments(ghComments)

	// Should have both the new comment and the GitHub comment
	if len(f.Comments[2]) != 2 {
		t.Errorf("Expected 2 comments on line 2, got %d", len(f.Comments[2]))
	}

	// Check that new comment was preserved
	var foundNew bool
	var foundGitHub bool
	for _, comment := range f.Comments[2] {
		if comment.IsNew && comment.Body == "My new comment" {
			foundNew = true
		}
		if !comment.IsNew && comment.Author == "reviewer" && comment.ID == 123 {
			foundGitHub = true
		}
	}

	if !foundNew {
		t.Error("New comment should be preserved")
	}
	if !foundGitHub {
		t.Error("GitHub comment should be added")
	}
}

func TestFileWithComments_SyncWithGitHubIssueComments(t *testing.T) {
	f := NewPRComments()

	// Add a new PR comment that should be preserved
	f.Comments[0] = []ReviewComment{
		{IsNew: true, Body: "My new PR comment", Author: ""},
	}

	// Create mock GitHub issue comments
	createdAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)
	ghComments := []*github.IssueComment{
		{
			ID:        github.Int64(456),
			User:      &github.User{Login: github.String("alice")},
			Body:      github.String("LGTM! Nice work on this PR."),
			CreatedAt: &github.Timestamp{Time: createdAt},
		},
	}

	f.SyncWithGitHubIssueComments(ghComments)

	// Should have both the new comment and the GitHub comment
	if len(f.Comments[0]) != 2 {
		t.Errorf("Expected 2 comments at line 0, got %d", len(f.Comments[0]))
	}

	// Check that new comment was preserved
	var foundNew bool
	var foundGitHub bool
	for _, comment := range f.Comments[0] {
		if comment.IsNew && comment.Body == "My new PR comment" {
			foundNew = true
		}
		if !comment.IsNew && comment.Author == "alice" && comment.ID == 456 {
			foundGitHub = true
		}
	}

	if !foundNew {
		t.Error("New PR comment should be preserved")
	}
	if !foundGitHub {
		t.Error("GitHub issue comment should be added")
	}
}

func TestWrapText_PreservesNewlines(t *testing.T) {
	text := "First paragraph.\n\nSecond paragraph after blank line.\n- Bullet 1\n- Bullet 2"

	wrapped := wrapText(text, 50, "")

	// Should preserve paragraph breaks
	if !strings.Contains(strings.Join(wrapped, "\n"), "\n\n") {
		t.Error("Should preserve blank lines between paragraphs")
	}

	// Should have bullet points on separate lines
	joinedResult := strings.Join(wrapped, "\n")
	if !strings.Contains(joinedResult, "- Bullet 1\n- Bullet 2") {
		t.Error("Should preserve bullet point formatting")
	}
}

func TestFormatCommentHeader(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)

	// Test with date and metadata
	r := ReviewComment{
		Author:    "reviewer",
		CreatedAt: &createdAt,
		IsFile:    true,
	}
	header := r.Format()
	expected := "by reviewer ─ date 2024-01-15 14:30 ─ file"
	if header != expected {
		t.Errorf("Expected %q, got %q", expected, header)
	}

	// Test without date
	r = ReviewComment{
		Author: "alice",
	}
	header = r.Format()
	expected = "by alice"
	if header != expected {
		t.Errorf("Expected %q, got %q", expected, header)
	}

	// Test with date but no metadata
	r = ReviewComment{
		Author:    "bob",
		CreatedAt: &createdAt,
	}
	header = r.Format()
	expected = "by bob ─ date 2024-01-15 14:30"
	if header != expected {
		t.Errorf("Expected %q, got %q", expected, header)
	}
}

func TestFileWithComments_ShorthandSyntax(t *testing.T) {
	content := `package main

import "fmt"

func example() {
	//++ This is a new shorthand comment.
	// This should be part of the comment.
	// So should this line.
	//
	// Paragraphs should be separated.
	//-- End of comment

	fmt.Println("Hello")
	//++ Another comment:
	// Multi-line comment
	// continues here.

	fmt.Println("Done")
}`

	f := NewFileWithComments("test.go")
	err := f.Parse(content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check that we found comments on the right lines
	// First comment should be attached to line after it was parsed (line 11 - "fmt.Println("Hello")")
	foundAtLine := -1
	for lineNum, comments := range f.Comments {
		for _, comment := range comments {
			if comment.IsNew && strings.Contains(comment.Body, "This is a new shorthand comment") {
				foundAtLine = lineNum
				expectedBody := "This is a new shorthand comment. This should be part of the comment. So should this line.\n\nParagraphs should be separated."
				if comment.Body != expectedBody {
					t.Errorf("First shorthand comment body:\nExpected: %q\nGot: %q", expectedBody, comment.Body)
				}
				t.Logf("Found first shorthand comment on line %d", lineNum)
				break
			}
		}
	}
	if foundAtLine != 5 {
		t.Errorf("Should have found first shorthand comment at line 5 (was %d)", foundAtLine)
	}

	// Check second comment
	foundAtLine = -1
	for lineNum, comments := range f.Comments {
		for _, comment := range comments {
			if comment.IsNew && strings.Contains(comment.Body, "Another comment") {
				foundAtLine = lineNum
				expectedBody := "Another comment: Multi-line comment continues here."
				if comment.Body != expectedBody {
					t.Errorf("Second shorthand comment body:\nExpected: %q\nGot: %q", expectedBody, comment.Body)
				}
				t.Logf("Found second shorthand comment on line %d", lineNum)
				break
			}
		}
	}
	if foundAtLine != 7 {
		t.Errorf("Should have found second shorthand comment at line 7 (was %d)", foundAtLine)
	}

	// Test serialization - should convert to normal format with NEW author
	serialized := f.Serialize()

	// Should contain NEW author in headers
	if !strings.Contains(serialized, "new") {
		t.Errorf("Serialized output should contain 'new' field, got:\n%s", serialized)
	}

	// Should not contain shorthand syntax in output
	if strings.Contains(serialized, "//++") {
		t.Errorf("Serialized output should not contain shorthand '//++' syntax, got:\n%s", serialized)
	}

	if strings.Contains(serialized, "//--") {
		t.Errorf("Serialized output should not contain shorthand '//--' syntax, got:\n%s", serialized)
	}

	t.Logf("Serialized output:\n%s", serialized)
}

func TestNewHeaderFormat(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		comment  ReviewComment
		expected string
	}{
		{
			name: "new comment",
			comment: ReviewComment{
				IsNew: true,
			},
			expected: "new",
		},
		{
			name: "comment with ID",
			comment: ReviewComment{
				ID:        12345,
				Author:    "alice",
				CreatedAt: &createdAt,
			},
			expected: "by alice ─ date 2024-01-15 14:30 ─ id 12345",
		},
		{
			name: "reply comment",
			comment: ReviewComment{
				ID:        67890,
				ParentID:  12345,
				Author:    "bob",
				CreatedAt: &createdAt,
			},
			expected: "by bob ─ date 2024-01-15 14:30 ─ id 67890 ─ parent 12345",
		},
		{
			name: "file-level comment",
			comment: ReviewComment{
				ID:        54321,
				Author:    "charlie",
				CreatedAt: &createdAt,
				IsFile:    true,
			},
			expected: "by charlie ─ date 2024-01-15 14:30 ─ id 54321 ─ file",
		},
		{
			name: "range comment",
			comment: ReviewComment{
				ID:        98765,
				Author:    "dave",
				CreatedAt: &createdAt,
				Line:      10,
				StartLine: 7,
			},
			expected: "by dave ─ date 2024-01-15 14:30 ─ id 98765 ─ range -4",
		},
		{
			name: "new comment with all fields",
			comment: ReviewComment{
				IsNew:     true,
				ID:        11111,
				ParentID:  22222,
				Line:      5,
				StartLine: 3,
				IsFile:    true,
			},
			expected: "new ─ id 11111 ─ parent 22222 ─ range -3 ─ file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.comment.Format()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParseHeaderFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		header   string
		expected ReviewComment
	}{
		{
			name:   "new comment",
			header: "new",
			expected: ReviewComment{
				IsNew: true,
			},
		},
		{
			name:   "comment with ID",
			header: "by alice ─ date 2024-01-15 14:30 ─ id 12345",
			expected: ReviewComment{
				Author:    "alice",
				CreatedAt: &createdAt,
				ID:        12345,
			},
		},
		{
			name:   "reply comment",
			header: "by bob ─ date 2024-01-15 14:30 ─ id 67890 ─ parent 12345",
			expected: ReviewComment{
				Author:    "bob",
				CreatedAt: &createdAt,
				ID:        67890,
				ParentID:  12345,
			},
		},
		{
			name:   "file-level comment",
			header: "by charlie ─ date 2024-01-15 14:30 ─ id 54321 ─ file",
			expected: ReviewComment{
				Author:    "charlie",
				CreatedAt: &createdAt,
				ID:        54321,
				IsFile:    true,
			},
		},
		{
			name:   "range comment",
			header: "by dave ─ date 2024-01-15 14:30 ─ id 98765 ─ range -4",
			expected: ReviewComment{
				Author:    "dave",
				CreatedAt: &createdAt,
				ID:        98765,
				StartLine: -4, // Temporary marker
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseHeaderFields(tt.header)
			
			// Check all fields
			if result.IsNew != tt.expected.IsNew {
				t.Errorf("IsNew: expected %v, got %v", tt.expected.IsNew, result.IsNew)
			}
			if result.Author != tt.expected.Author {
				t.Errorf("Author: expected %q, got %q", tt.expected.Author, result.Author)
			}
			if result.ID != tt.expected.ID {
				t.Errorf("ID: expected %d, got %d", tt.expected.ID, result.ID)
			}
			if result.ParentID != tt.expected.ParentID {
				t.Errorf("ParentID: expected %d, got %d", tt.expected.ParentID, result.ParentID)
			}
			if result.IsFile != tt.expected.IsFile {
				t.Errorf("IsFile: expected %v, got %v", tt.expected.IsFile, result.IsFile)
			}
			if result.StartLine != tt.expected.StartLine {
				t.Errorf("StartLine: expected %d, got %d", tt.expected.StartLine, result.StartLine)
			}
			
			// Check CreatedAt
			if tt.expected.CreatedAt == nil {
				if result.CreatedAt != nil {
					t.Errorf("CreatedAt: expected nil, got %v", result.CreatedAt)
				}
			} else {
				if result.CreatedAt == nil {
					t.Error("CreatedAt: expected non-nil, got nil")
				} else if !result.CreatedAt.Equal(*tt.expected.CreatedAt) {
					t.Errorf("CreatedAt: expected %v, got %v", tt.expected.CreatedAt, result.CreatedAt)
				}
			}
		})
	}
}

func TestTrailingNewlinePreservation(t *testing.T) {
	tests := []struct {
		name                string
		content             string
		expectTrailingNewline bool
	}{
		{
			name:                "content with trailing newline",
			content:             "package main\n\nfunc hello() {}\n",
			expectTrailingNewline: true,
		},
		{
			name:                "content without trailing newline",
			content:             "package main\n\nfunc hello() {}",
			expectTrailingNewline: false,
		},
		{
			name:                "empty content",
			content:             "",
			expectTrailingNewline: false,
		},
		{
			name:                "single newline",
			content:             "\n",
			expectTrailingNewline: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFileWithComments("test.go")
			err := f.Parse(tt.content)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			// Check that HasTrailingNewline was set correctly
			if f.HasTrailingNewline != tt.expectTrailingNewline {
				t.Errorf("HasTrailingNewline: expected %v, got %v", tt.expectTrailingNewline, f.HasTrailingNewline)
			}

			// Serialize and check if trailing newline is preserved
			serialized := f.Serialize()
			hasTrailingNewline := len(serialized) > 0 && strings.HasSuffix(serialized, "\n")
			
			if hasTrailingNewline != tt.expectTrailingNewline {
				t.Errorf("Serialized trailing newline: expected %v, got %v", tt.expectTrailingNewline, hasTrailingNewline)
				t.Logf("Serialized content: %q", serialized)
			}
		})
	}
}
