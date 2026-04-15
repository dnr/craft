package main

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClearCraftContent(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		input    string
		expected string
		changed  bool
	}{
		{
			name:     "no craft comments",
			path:     "clean.go",
			input:    "package main\n\nfunc main() {\n}\n",
			expected: "package main\n\nfunc main() {\n}\n",
			changed:  false,
		},
		{
			name: "inline craft comments",
			path: "code.go",
			input: "package main\n" +
				"\n" +
				"func main() {\n" +
				"\tx := 1\n" +
				"\t// ╓───── @alice ─ at 2025-01-15 12:34 ─ prrc kwDOPgi5ks6AAA111\n" +
				"\t// ║ Nice variable!\n" +
				"\ty := 2\n" +
				"}\n",
			expected: "package main\n" +
				"\n" +
				"func main() {\n" +
				"\tx := 1\n" +
				"\ty := 2\n" +
				"}\n",
			changed: true,
		},
		{
			name: "thread with reply",
			path: "code.go",
			input: "line 1\n" +
				"// ╓───── @alice ─ at 2025-01-15 12:34 ─ prrc kwDOPgi5ks6AAA111\n" +
				"// ║ First comment\n" +
				"// ╟───── @bob ─ at 2025-01-15 13:00 ─ prrc kwDOPgi5ks6BBB222\n" +
				"// ║ Reply\n" +
				"line 2\n",
			expected: "line 1\n" +
				"line 2\n",
			changed: true,
		},
		{
			name: "outdated comments section",
			path: "code.go",
			input: "package main\n" +
				"\n" +
				"func main() {\n" +
				"}\n" +
				"\n" +
				"// ━━━━━━━━━ outdated comments\n" +
				"// ╓───── @alice ─ at 2025-01-15 12:34 ─ outdated ─ origline 42 ─ prrc kwDOPgi5ks6AAA111\n" +
				"// ║ Old comment\n",
			expected: "package main\n" +
				"\n" +
				"func main() {\n" +
				"}\n",
			changed: true,
		},
		{
			name: "both inline and outdated",
			path: "code.go",
			input: "line 1\n" +
				"// ╓───── @alice ─ at 2025-01-15 12:34 ─ prrc kwDOPgi5ks6AAA111\n" +
				"// ║ Inline comment\n" +
				"line 2\n" +
				"\n" +
				"// ━━━━━━━━━ outdated comments\n" +
				"// ╓───── @bob ─ at 2025-01-15 13:00 ─ outdated ─ origline 10 ─ prrc kwDOPgi5ks6BBB222\n" +
				"// ║ Outdated comment\n",
			expected: "line 1\n" +
				"line 2\n",
			changed: true,
		},
		{
			name: "python file",
			path: "code.py",
			input: "def foo():\n" +
				"    x = 1\n" +
				"    # ╓───── @alice ─ at 2025-01-15 12:34 ─ prrc kwDOPgi5ks6AAA111\n" +
				"    # ║ Comment on python\n" +
				"    return x\n",
			expected: "def foo():\n" +
				"    x = 1\n" +
				"    return x\n",
			changed: true,
		},
		{
			name: "new (unsent) comments also cleared",
			path: "code.go",
			input: "line 1\n" +
				"// ╓───── new\n" +
				"// ║ My draft comment\n" +
				"line 2\n",
			expected: "line 1\n" +
				"line 2\n",
			changed: true,
		},
		{
			name: "preserves regular code comments",
			path: "code.go",
			input: "package main\n" +
				"\n" +
				"// This is a regular comment\n" +
				"func main() {\n" +
				"\t// TODO: refactor this\n" +
				"\tx := 1\n" +
				"\t// ╓───── @alice ─ at 2025-01-15 12:34 ─ prrc kwDOPgi5ks6AAA111\n" +
				"\t// ║ Craft comment\n" +
				"}\n",
			expected: "package main\n" +
				"\n" +
				"// This is a regular comment\n" +
				"func main() {\n" +
				"\t// TODO: refactor this\n" +
				"\tx := 1\n" +
				"}\n",
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, changed := clearCraftContent(tt.input, tt.path)
			assert.Equal(t, tt.changed, changed, "changed")
			if changed {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestClearCraftContentRoundTrip(t *testing.T) {
	// Serialize a PR with comments, then clear should restore original code.
	original := "package main\n\nfunc main() {\n\tx := 1\n\ty := 2\n}\n"

	pr := &PullRequest{
		ID:         "PR_test",
		Number:     1,
		HeadRefOID: "abcd1234",
		ReviewThreads: []ReviewThread{
			{
				ID:          "PRRT_1",
				Path:        "main.go",
				DiffSide:    DiffSideRight,
				Line:        4,
				SubjectType: SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_1",
						Author:    Actor{Login: "alice"},
						Body:      "Consider renaming this variable",
						CreatedAt: time.Date(2025, 1, 15, 12, 34, 0, 0, time.UTC),
					},
					{
						ID:        "PRRC_2",
						Author:    Actor{Login: "bob"},
						Body:      "I agree with alice",
						CreatedAt: time.Date(2025, 1, 15, 13, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	memfs := fstest.MapFS{"main.go": &fstest.MapFile{Data: []byte(original)}}
	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	// File should now have craft comments
	serialized := string(memfs["main.go"].Data)
	assert.Contains(t, serialized, "╓")
	assert.NotEqual(t, original, serialized)

	// Clear should restore original
	cleared, changed := clearCraftContent(serialized, "main.go")
	assert.True(t, changed)
	assert.Equal(t, original, cleared)
}

func TestClearCraftContentWithOutdatedRoundTrip(t *testing.T) {
	// Serialize a PR with outdated comments, then clear.
	original := "line 1\nline 2\nline 3\n"

	pr := &PullRequest{
		ID:         "PR_test",
		Number:     1,
		HeadRefOID: "abcd1234",
		ReviewThreads: []ReviewThread{
			{
				ID:           "PRRT_1",
				Path:         "file.go",
				DiffSide:     DiffSideRight,
				Line:         0, // out of bounds -> outdated
				OriginalLine: 50,
				SubjectType:  SubjectTypeLine,
				Comments: []ReviewComment{
					{
						ID:        "PRRC_1",
						Author:    Actor{Login: "alice"},
						Body:      "Outdated comment",
						CreatedAt: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	memfs := fstest.MapFS{"file.go": &fstest.MapFile{Data: []byte(original)}}
	opts := SerializeOptions{FS: memfs}
	err := Serialize(pr, opts)
	require.NoError(t, err)

	serialized := string(memfs["file.go"].Data)
	assert.Contains(t, serialized, outdatedCommentsHeader)

	cleared, changed := clearCraftContent(serialized, "file.go")
	assert.True(t, changed)
	assert.Equal(t, original, cleared)
}

func TestCollectNewCommentsReplyOnly(t *testing.T) {
	// Test that CollectNewComments returns new threads (which --reply-only would reject)
	pr := &PullRequest{
		ReviewThreads: []ReviewThread{
			{
				// Existing thread with a new reply
				Path:     "file.go",
				Line:     10,
				DiffSide: DiffSideRight,
				Comments: []ReviewComment{
					{
						ID:     "PRRC_existing",
						Author: Actor{Login: "alice"},
						Body:   "Original comment",
					},
					{
						IsNew: true,
						Body:  "My reply",
					},
				},
			},
			{
				// New thread (no ID on first comment)
				Path:     "file.go",
				Line:     20,
				DiffSide: DiffSideRight,
				Comments: []ReviewComment{
					{
						IsNew: true,
						Body:  "New thread comment",
					},
				},
			},
		},
	}

	review, err := CollectNewComments(pr)
	require.NoError(t, err)

	// Should have both a reply and a new thread
	assert.Len(t, review.Replies, 1)
	assert.Len(t, review.NewThreads, 1)
	assert.Equal(t, "My reply", review.Replies[0].Body)
	assert.Equal(t, "New thread comment", review.NewThreads[0].Body)
}

func TestCollectNewCommentsRepliesOnly(t *testing.T) {
	// When there are only replies, --reply-only should work fine
	pr := &PullRequest{
		ReviewThreads: []ReviewThread{
			{
				Path:     "file.go",
				Line:     10,
				DiffSide: DiffSideRight,
				Comments: []ReviewComment{
					{
						ID:     "PRRC_existing",
						Author: Actor{Login: "alice"},
						Body:   "Original comment",
					},
					{
						IsNew: true,
						Body:  "Reply 1",
					},
					{
						IsNew: true,
						Body:  "Reply 2",
					},
				},
			},
		},
	}

	review, err := CollectNewComments(pr)
	require.NoError(t, err)

	assert.Len(t, review.NewThreads, 0)
	assert.Len(t, review.Replies, 2)
}
