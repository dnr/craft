package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiff(t *testing.T) {
	tests := []struct {
		name     string
		diff     string
		expected []Hunk
	}{
		{
			name: "single line change",
			diff: `diff --git a/foo.go b/foo.go
index abc123..def456 100644
--- a/foo.go
+++ b/foo.go
@@ -5 +5 @@ func foo() {
-    old line
+    new line`,
			expected: []Hunk{
				{
					OldStart: 5, OldCount: 1,
					NewStart: 5, NewCount: 1,
					OldLines: []string{"    old line"},
					NewLines: []string{"    new line"},
				},
			},
		},
		{
			name: "multi-line change",
			diff: `@@ -10,3 +10,2 @@
-line 1
-line 2
-line 3
+new line 1
+new line 2`,
			expected: []Hunk{
				{
					OldStart: 10, OldCount: 3,
					NewStart: 10, NewCount: 2,
					OldLines: []string{"line 1", "line 2", "line 3"},
					NewLines: []string{"new line 1", "new line 2"},
				},
			},
		},
		{
			name: "pure addition",
			diff: `@@ -5,0 +6,2 @@
+added line 1
+added line 2`,
			expected: []Hunk{
				{
					OldStart: 5, OldCount: 0,
					NewStart: 6, NewCount: 2,
					OldLines: nil,
					NewLines: []string{"added line 1", "added line 2"},
				},
			},
		},
		{
			name: "pure deletion",
			diff: `@@ -5,2 +4,0 @@
-deleted line 1
-deleted line 2`,
			expected: []Hunk{
				{
					OldStart: 5, OldCount: 2,
					NewStart: 4, NewCount: 0,
					OldLines: []string{"deleted line 1", "deleted line 2"},
					NewLines: nil,
				},
			},
		},
		{
			name: "multiple hunks",
			diff: `@@ -5 +5 @@
-old1
+new1
@@ -20 +20 @@
-old2
+new2`,
			expected: []Hunk{
				{
					OldStart: 5, OldCount: 1,
					NewStart: 5, NewCount: 1,
					OldLines: []string{"old1"},
					NewLines: []string{"new1"},
				},
				{
					OldStart: 20, OldCount: 1,
					NewStart: 20, NewCount: 1,
					OldLines: []string{"old2"},
					NewLines: []string{"new2"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hunks := parseUnifiedDiff(tt.diff)

			require.Len(t, hunks, len(tt.expected))

			for i, got := range hunks {
				want := tt.expected[i]
				assert.Equal(t, want.OldStart, got.OldStart, "hunk %d OldStart", i)
				assert.Equal(t, want.OldCount, got.OldCount, "hunk %d OldCount", i)
				assert.Equal(t, want.NewStart, got.NewStart, "hunk %d NewStart", i)
				assert.Equal(t, want.NewCount, got.NewCount, "hunk %d NewCount", i)
				assert.Equal(t, want.OldLines, got.OldLines, "hunk %d OldLines", i)
				assert.Equal(t, want.NewLines, got.NewLines, "hunk %d NewLines", i)
			}
		})
	}
}

func TestClassifyHunk(t *testing.T) {
	goStyle := commentStyle{linePrefix: "//"}

	tests := []struct {
		name     string
		hunk     Hunk
		style    commentStyle
		expected HunkClassification
	}{
		{
			name: "code change -> suggestion",
			hunk: Hunk{
				OldLines: []string{"    old code"},
				NewLines: []string{"    new code"},
			},
			style:    goStyle,
			expected: HunkSuggestion,
		},
		{
			name: "pure deletion -> suggestion",
			hunk: Hunk{
				OldLines: []string{"    deleted line"},
				NewLines: nil,
			},
			style:    goStyle,
			expected: HunkSuggestion,
		},
		{
			name: "pure code comment addition -> code comment",
			hunk: Hunk{
				OldLines: nil,
				NewLines: []string{"    // this is a comment"},
			},
			style:    goStyle,
			expected: HunkCodeComment,
		},
		{
			name: "multiple code comments -> code comment",
			hunk: Hunk{
				OldLines: nil,
				NewLines: []string{"    // comment 1", "    // comment 2"},
			},
			style:    goStyle,
			expected: HunkCodeComment,
		},
		{
			name: "pure code addition -> warn",
			hunk: Hunk{
				OldLines: nil,
				NewLines: []string{"    newFunction()"},
			},
			style:    goStyle,
			expected: HunkWarnPureAdd,
		},
		{
			name: "mixed code and comment addition -> warn",
			hunk: Hunk{
				OldLines: nil,
				NewLines: []string{"    // comment", "    code()"},
			},
			style:    goStyle,
			expected: HunkWarnPureAdd,
		},
		{
			name: "craft comment only -> preserve",
			hunk: Hunk{
				OldLines: nil,
				NewLines: []string{"// ╓───── new", "// ║ hello"},
			},
			style:    goStyle,
			expected: HunkCraftComment,
		},
		{
			name: "code change with craft comment -> warn mixed",
			hunk: Hunk{
				OldLines: []string{"    old code"},
				NewLines: []string{"    new code", "// ╓───── new"},
			},
			style:    goStyle,
			expected: HunkWarnMixed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hunk := tt.hunk
			classifyHunk(&hunk, tt.style)
			assert.Equal(t, tt.expected, hunk.Classification)
		})
	}
}

func TestIsCraftCommentLine(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"// ╓───── new", true},
		{"// ╟───── reply", true},
		{"// ║ body text", true},
		{"// regular comment", false},
		{"    code();", false},
		{"", false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, isCraftCommentLine(tt.line), "line: %q", tt.line)
	}
}

func TestIsCodeCommentLine(t *testing.T) {
	goStyle := commentStyle{linePrefix: "//"}
	pyStyle := commentStyle{linePrefix: "#"}

	tests := []struct {
		line     string
		style    commentStyle
		expected bool
	}{
		{"// comment", goStyle, true},
		{"    // indented comment", goStyle, true},
		{"code()", goStyle, false},
		{"# python comment", pyStyle, true},
		{"    # indented", pyStyle, true},
		{"code", pyStyle, false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, isCodeCommentLine(tt.line, tt.style), "line: %q, prefix: %q", tt.line, tt.style.linePrefix)
	}
}

// generateDiff shells out to diff to produce a unified diff between two strings.
// Returns empty string if files are identical.
func generateDiff(t *testing.T, before, after string) string {
	t.Helper()

	cmd := exec.Command("diff", "-U0", "/dev/fd/3", "/dev/fd/4")

	beforeR, beforeW, _ := os.Pipe()
	afterR, afterW, _ := os.Pipe()
	cmd.ExtraFiles = []*os.File{beforeR, afterR}

	go func() { beforeW.WriteString(before); beforeW.Close() }()
	go func() { afterW.WriteString(after); afterW.Close() }()

	out, _ := cmd.Output() // diff returns non-zero when files differ
	beforeR.Close()
	afterR.Close()
	return string(out)
}

func TestTransformFileWithSuggestions(t *testing.T) {
	// This is a golden test that exercises the full transformation pipeline.
	// It tests:
	// 1. Code change -> suggestion
	// 2. Code comment addition -> craft comment
	// 3. Pure code addition -> warning (skipped)
	// 4. Existing craft comments -> skipped (not copied to output currently)

	before := `package main

func example() {
	oldCode := "this will be changed"
	keepThis := true
	alsoKeep := false
}
`

	after := `package main

func example() {
	newCode := "this was changed"
	keepThis := true
	// This is a review comment
	alsoKeep := false
	pureAddition := "this is new code"
}
`

	// Note: craft comments should match the indentation of the code they follow
	expected := `package main

func example() {
	oldCode := "this will be changed"
	// ╓───── new
	// ║ ` + "```" + `suggestion
	// ║ 	newCode := "this was changed"
	// ║ ` + "```" + `
	keepThis := true
	// ╓───── new
	// ║ This is a review comment
	alsoKeep := false
}
`

	diff := generateDiff(t, before, after)
	require.NotEmpty(t, diff, "diff generated empty output")

	result := transformFileWithSuggestions(before, diff, "test.go")

	assert.Equal(t, 1, result.Stats.suggestions)
	assert.Equal(t, 1, result.Stats.craftComments)
	assert.Equal(t, 1, result.Stats.warnings)
	assert.Equal(t, expected, result.Content)
}

func TestTransformMultiLineChange(t *testing.T) {
	before := `func foo() {
	line1 := 1
	line2 := 2
	line3 := 3
}
`

	after := `func foo() {
	newLine1 := "a"
	newLine2 := "b"
}
`

	// range -2 means 3 lines are being replaced (range = -(OldCount-1))
	// headerFieldSep is " ─ " so header looks like "new ─ range -2"
	expected := "func foo() {\n" +
		"\tline1 := 1\n" +
		"\tline2 := 2\n" +
		"\tline3 := 3\n" +
		"\t// ╓───── new" + headerFieldSep + "range -2\n" +
		"\t// ║ ```suggestion\n" +
		"\t// ║ \tnewLine1 := \"a\"\n" +
		"\t// ║ \tnewLine2 := \"b\"\n" +
		"\t// ║ ```\n" +
		"}\n"

	diff := generateDiff(t, before, after)
	result := transformFileWithSuggestions(before, diff, "test.go")

	assert.Equal(t, 1, result.Stats.suggestions)
	assert.Equal(t, expected, result.Content)
}

func TestTransformDeletion(t *testing.T) {
	before := `func foo() {
	keep := 1
	delete1 := 2
	delete2 := 3
	alsoKeep := 4
}
`

	after := `func foo() {
	keep := 1
	alsoKeep := 4
}
`

	expected := `func foo() {
	keep := 1
	delete1 := 2
	delete2 := 3
	// ╓───── new` + headerFieldSep + `range -1
	// ║ ` + "```" + `suggestion
	// ║ ` + "```" + `
	alsoKeep := 4
}
`

	diff := generateDiff(t, before, after)
	result := transformFileWithSuggestions(before, diff, "test.go")

	assert.Equal(t, 1, result.Stats.suggestions)
	assert.Equal(t, expected, result.Content)
}

func TestTransformPythonFile(t *testing.T) {
	before := `def example():
    old_code = "change me"
    return True
`

	after := `def example():
    new_code = "changed"
    return True
`

	expected := `def example():
    old_code = "change me"
    # ╓───── new
    # ║ ` + "```" + `suggestion
    # ║     new_code = "changed"
    # ║ ` + "```" + `
    return True
`

	diff := generateDiff(t, before, after)
	result := transformFileWithSuggestions(before, diff, "test.py")

	assert.Equal(t, 1, result.Stats.suggestions)
	assert.Equal(t, expected, result.Content)
}

func TestTransformCodeCommentAlone(t *testing.T) {
	before := `func foo() {
	x := 1
	y := 2
}
`

	after := `func foo() {
	x := 1
	// this is a review comment
	y := 2
}
`

	expected := `func foo() {
	x := 1
	// ╓───── new
	// ║ this is a review comment
	y := 2
}
`

	diff := generateDiff(t, before, after)
	result := transformFileWithSuggestions(before, diff, "test.go")

	assert.Equal(t, 0, result.Stats.suggestions)
	assert.Equal(t, 1, result.Stats.craftComments)
	assert.Equal(t, expected, result.Content)
}

func TestTransformWarnsMixedCraftAndCodeChanges(t *testing.T) {
	before := `func foo() {
	x := 1
	y := 2
}
`

	// User added a craft comment adjacent to a code edit - these get combined
	// into one hunk by diff, and we warn about it
	after := `func foo() {
	x := 1
	// ╓───── new
	// ║ existing craft comment
	newY := 22
}
`

	diff := generateDiff(t, before, after)
	result := transformFileWithSuggestions(before, diff, "test.go")

	// The mixed hunk should be skipped with a warning
	assert.Equal(t, 0, result.Stats.suggestions)
	assert.Equal(t, 1, result.Stats.warnings)
	assert.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "craft comments mixed with code changes")
}

func TestTransformPreservesPureCraftComments(t *testing.T) {
	before := `func foo() {
	x := 1
	y := 2
}
`

	// User added a craft comment NOT adjacent to any code changes
	after := `func foo() {
	// ╓───── new
	// ║ a standalone craft comment
	x := 1
	y := 2
}
`

	expected := `func foo() {
	// ╓───── new
	// ║ a standalone craft comment
	x := 1
	y := 2
}
`

	diff := generateDiff(t, before, after)
	result := transformFileWithSuggestions(before, diff, "test.go")

	assert.Equal(t, 0, result.Stats.suggestions)
	assert.Equal(t, 1, result.Stats.craftComments) // Preserved craft comment counts
	assert.Equal(t, 0, result.Stats.warnings)
	assert.Equal(t, expected, result.Content)
}
