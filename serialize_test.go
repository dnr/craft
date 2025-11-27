package main

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		header Header
	}{
		{
			name: "basic comment",
			header: Header{
				Author:    "alice",
				Timestamp: time.Date(2025, 1, 15, 12, 34, 0, 0, time.UTC),
				NodeID:    "PRRC_kwDOPgi5ks6ZBMOo",
			},
		},
		{
			name: "new comment",
			header: Header{
				IsNew: true,
			},
		},
		{
			name: "file comment",
			header: Header{
				Author:    "bob",
				Timestamp: time.Date(2025, 2, 20, 8, 0, 0, 0, time.UTC),
				NodeID:    "PRRC_kwDOPgi5ks6ABC123",
				IsFile:    true,
			},
		},
		{
			name: "range comment",
			header: Header{
				Author:    "carol",
				Timestamp: time.Date(2025, 3, 10, 16, 45, 0, 0, time.UTC),
				NodeID:    "PRRC_kwDOPgi5ks6XYZ789",
				Range:     -5,
			},
		},
		{
			name: "thread marker",
			header: Header{
				Author:    "dave",
				Timestamp: time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC),
				NodeID:    "PRRC_kwDOPgi5ks6DEF456",
				IsThread:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted := formatHeader(tt.header)
			parsed, ok := parseHeader(formatted)
			require.True(t, ok, "parseHeader should succeed")

			assert.Equal(t, tt.header.Author, parsed.Author)
			assert.Equal(t, tt.header.Timestamp.Unix(), parsed.Timestamp.Unix())
			assert.Equal(t, tt.header.NodeID, parsed.NodeID)
			assert.Equal(t, tt.header.IsNew, parsed.IsNew)
			assert.Equal(t, tt.header.IsFile, parsed.IsFile)
			assert.Equal(t, tt.header.Range, parsed.Range)
			assert.Equal(t, tt.header.IsThread, parsed.IsThread)
		})
	}
}

func TestNodeIDFormat(t *testing.T) {
	tests := []struct {
		full  string
		short string
	}{
		{"PRRC_kwDOPgi5ks6ZBMOo", "prrc kwDOPgi5ks6ZBMOo"},
		{"IC_kwDOPgi5ks1234567", "ic kwDOPgi5ks1234567"},
		{"PR_kwDOPgi5ks6k-agY", "pr kwDOPgi5ks6k-agY"},
		{"PRRT_kwDOPgi5ks5YVUJi", "prrt kwDOPgi5ks5YVUJi"},
	}

	for _, tt := range tests {
		t.Run(tt.full, func(t *testing.T) {
			assert.Equal(t, tt.short, formatNodeID(tt.full))
			assert.Equal(t, tt.full, parseNodeID(tt.short))
		})
	}
}

func TestDataRoundTrip(t *testing.T) {
	// Create test PR data
	pr := &PullRequest{
		ID:         "PR_kwDOPgi5ks6k-agY",
		Number:     42,
		HeadRefOID: "e6be80e7693c38dbdb464c92722f5e731df69993",
		ReviewThreads: []ReviewThread{
			{
				ID:          "PRRT_kwDOPgi5ks5YVUJi",
				Path:        "main.go",
				DiffSide:    DiffSideRight,
				Line:        10,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_kwDOPgi5ks6IymTJ",
						Author:    Actor{Login: "alice"},
						Body:      "This looks good!",
						CreatedAt: time.Date(2025, 1, 15, 12, 34, 0, 0, time.UTC),
						UpdatedAt: time.Date(2025, 1, 15, 12, 34, 0, 0, time.UTC),
					},
					{
						ID:        "PRRC_kwDOPgi5ks6ZBO2r",
						Author:    Actor{Login: "bob"},
						Body:      "Thanks for the review!",
						CreatedAt: time.Date(2025, 1, 15, 14, 0, 0, 0, time.UTC),
						UpdatedAt: time.Date(2025, 1, 15, 14, 0, 0, 0, time.UTC),
					},
				},
			},
			{
				ID:          "PRRT_kwDOPgi5ks5jw2bR",
				Path:        "util.go",
				DiffSide:    DiffSideRight,
				Line:        5,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_kwDOPgi5ks6ZBMOo",
						Author:    Actor{Login: "carol"},
						Body:      "Consider adding a comment here.",
						CreatedAt: time.Date(2025, 1, 16, 9, 0, 0, 0, time.UTC),
						UpdatedAt: time.Date(2025, 1, 16, 9, 0, 0, 0, time.UTC),
					},
				},
			},
		},
		IssueComments: []IssueComment{
			{
				ID:        "IC_kwDOPgi5ks1234567",
				Author:    Actor{Login: "dave"},
				Body:      "Overall LGTM!",
				CreatedAt: time.Date(2025, 1, 17, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2025, 1, 17, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	// Create in-memory filesystem with source files
	memfs := fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import "fmt"

func main() {
	fmt.Println("line 6")
	fmt.Println("line 7")
	fmt.Println("line 8")
	fmt.Println("line 9")
	fmt.Println("line 10")
	fmt.Println("line 11")
}
`),
		},
		"util.go": &fstest.MapFile{
			Data: []byte(`package main

func helper() {
	// line 4
	return // line 5
}
`),
		},
	}

	// Serialize
	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	// Verify PR-STATE.txt was created
	_, ok := memfs[prStateFile]
	require.True(t, ok, "PR-STATE.txt should exist")

	// Deserialize
	pr2, err := Deserialize(opts)
	require.NoError(t, err)

	// Compare key fields
	assert.Equal(t, pr.ID, pr2.ID)
	assert.Equal(t, pr.Number, pr2.Number)
	assert.Equal(t, pr.HeadRefOID, pr2.HeadRefOID)

	// Compare threads
	require.Len(t, pr2.ReviewThreads, len(pr.ReviewThreads))
	for i, thread := range pr.ReviewThreads {
		t2 := pr2.ReviewThreads[i]
		assert.Equal(t, thread.Path, t2.Path)
		assert.Equal(t, thread.Line, t2.Line)
		assert.Equal(t, thread.SubjectType, t2.SubjectType)

		require.Len(t, t2.Comments, len(thread.Comments))
		for j, comment := range thread.Comments {
			c2 := t2.Comments[j]
			assert.Equal(t, comment.ID, c2.ID)
			assert.Equal(t, comment.Author.Login, c2.Author.Login)
			assert.Equal(t, comment.Body, c2.Body)
		}
	}

	// Compare issue comments
	require.Len(t, pr2.IssueComments, len(pr.IssueComments))
	for i, comment := range pr.IssueComments {
		c2 := pr2.IssueComments[i]
		assert.Equal(t, comment.ID, c2.ID)
		assert.Equal(t, comment.Author.Login, c2.Author.Login)
		assert.Equal(t, comment.Body, c2.Body)
	}
}

func TestFileRoundTrip(t *testing.T) {
	// Start with files that already have craft comments
	mainGoWithComments := `package main

import "fmt"

func main() {
	fmt.Println("hello")
// ❯ ───── by alice ─ at 2025-01-15 12:34 ─ prrc kwDOPgi5ks6IymTJ ─────
// ❯ Nice print statement!
	fmt.Println("world")
}
`
	prState := `───── pr ─ number 42 ─ pr kwDOPgi5ks6k-agY ─ head abc123 ─────

───── by dave ─ at 2025-01-17 10:00 ─ ic kwDOPgi5ks1234567 ─────
Overall LGTM!

`

	memfs := fstest.MapFS{
		"main.go":   &fstest.MapFile{Data: []byte(mainGoWithComments)},
		prStateFile: &fstest.MapFile{Data: []byte(prState)},
	}

	// Deserialize
	opts := SerializeOptions{FS: memfs}
	pr, err := Deserialize(opts)
	require.NoError(t, err)

	// Verify we got the data
	assert.Equal(t, 42, pr.Number)
	require.Len(t, pr.ReviewThreads, 1)
	assert.Equal(t, "main.go", pr.ReviewThreads[0].Path)
	assert.Equal(t, 6, pr.ReviewThreads[0].Line) // Comment is after line 6
	assert.Equal(t, "Nice print statement!", pr.ReviewThreads[0].Comments[0].Body)
	require.Len(t, pr.IssueComments, 1)
	assert.Equal(t, "Overall LGTM!", pr.IssueComments[0].Body)

	// Serialize right on top of existing files (should be idempotent)
	err = Serialize(pr, opts)
	require.NoError(t, err)

	// Check for exact byte match
	assert.Equal(t, mainGoWithComments, string(memfs["main.go"].Data))
	assert.Equal(t, prState, string(memfs[prStateFile].Data))
}

func TestNewCommentRoundTrip(t *testing.T) {
	// Test that new comments (isNew: true) round-trip correctly
	pr := &PullRequest{
		ID:         "PR_kwDOPgi5ks6k-agY",
		Number:     99,
		HeadRefOID: "deadbeef",
		ReviewThreads: []ReviewThread{
			{
				Path:        "test.go",
				DiffSide:    DiffSideRight,
				Line:        3,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						Body:  "This is a new comment",
						IsNew: true,
					},
				},
			},
		},
	}

	memfs := fstest.MapFS{
		"test.go": &fstest.MapFile{
			Data: []byte("line 1\nline 2\nline 3\nline 4\n"),
		},
	}

	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	pr2, err := Deserialize(opts)
	require.NoError(t, err)

	require.Len(t, pr2.ReviewThreads, 1)
	require.Len(t, pr2.ReviewThreads[0].Comments, 1)
	assert.True(t, pr2.ReviewThreads[0].Comments[0].IsNew)
	assert.Equal(t, "This is a new comment", pr2.ReviewThreads[0].Comments[0].Body)
}

func TestMultipleThreadsSameLine(t *testing.T) {
	// Test multiple threads on the same line
	pr := &PullRequest{
		ID:         "PR_kwDOPgi5ks6k-agY",
		Number:     100,
		HeadRefOID: "cafebabe",
		ReviewThreads: []ReviewThread{
			{
				ID:          "PRRT_1",
				Path:        "code.go",
				DiffSide:    DiffSideRight,
				Line:        2,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_1",
						Author:    Actor{Login: "alice"},
						Body:      "First thread comment",
						CreatedAt: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
					},
				},
			},
			{
				ID:          "PRRT_2",
				Path:        "code.go",
				DiffSide:    DiffSideRight,
				Line:        2,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_2",
						Author:    Actor{Login: "bob"},
						Body:      "Second thread comment",
						CreatedAt: time.Date(2025, 1, 1, 11, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	memfs := fstest.MapFS{
		"code.go": &fstest.MapFile{
			Data: []byte("line 1\nline 2\nline 3\n"),
		},
	}

	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	pr2, err := Deserialize(opts)
	require.NoError(t, err)

	// Should have 2 threads
	require.Len(t, pr2.ReviewThreads, 2)
	assert.Equal(t, "First thread comment", pr2.ReviewThreads[0].Comments[0].Body)
	assert.Equal(t, "Second thread comment", pr2.ReviewThreads[1].Comments[0].Body)
}

func TestMultilineCommentBody(t *testing.T) {
	pr := &PullRequest{
		ID:         "PR_test",
		Number:     1,
		HeadRefOID: "abcd1234",
		ReviewThreads: []ReviewThread{
			{
				ID:          "PRRT_1",
				Path:        "file.go",
				DiffSide:    DiffSideRight,
				Line:        1,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_1",
						Author:    Actor{Login: "alice"},
						Body:      "Line one\nLine two\nLine three",
						CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	memfs := fstest.MapFS{
		"file.go": &fstest.MapFile{
			Data: []byte("code here\n"),
		},
	}

	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	pr2, err := Deserialize(opts)
	require.NoError(t, err)

	require.Len(t, pr2.ReviewThreads, 1)
	assert.Equal(t, "Line one\nLine two\nLine three", pr2.ReviewThreads[0].Comments[0].Body)
}

func TestPreservesTrailingNewline(t *testing.T) {
	// File with trailing newline
	withNewline := "line 1\nline 2\n"
	// File without trailing newline
	withoutNewline := "line 1\nline 2"

	pr := &PullRequest{
		ID:         "PR_test",
		Number:     1,
		HeadRefOID: "1234",
		ReviewThreads: []ReviewThread{
			{
				Path:        "with.go",
				DiffSide:    DiffSideRight,
				Line:        1,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{ID: "PRRC_1", Author: Actor{Login: "a"}, Body: "comment"},
				},
			},
			{
				Path:        "without.go",
				DiffSide:    DiffSideRight,
				Line:        1,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{ID: "PRRC_2", Author: Actor{Login: "b"}, Body: "comment"},
				},
			},
		},
	}

	memfs := fstest.MapFS{
		"with.go":    &fstest.MapFile{Data: []byte(withNewline)},
		"without.go": &fstest.MapFile{Data: []byte(withoutNewline)},
	}

	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	// Check that trailing newline status is preserved
	withData := string(memfs["with.go"].Data)
	withoutData := string(memfs["without.go"].Data)

	assert.True(t, withData[len(withData)-1] == '\n', "should preserve trailing newline")
	assert.True(t, withoutData[len(withoutData)-1] != '\n', "should preserve no trailing newline")
}
