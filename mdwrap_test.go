package main

import (
	"strings"
	"testing"

	"rsc.io/markdown"
)

func TestUnwrap(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single line",
			input: "Hello world",
			want:  "Hello world\n",
		},
		{
			name:  "soft wrapped",
			input: "Hello\nworld",
			want:  "Hello world\n",
		},
		{
			name:  "multiple soft wraps",
			input: "This is a\nlong paragraph\nthat spans\nmultiple lines",
			want:  "This is a long paragraph that spans multiple lines\n",
		},
		{
			name:  "preserves paragraphs",
			input: "First paragraph.\n\nSecond paragraph.",
			want:  "First paragraph.\n\nSecond paragraph.\n",
		},
		{
			name:  "preserves hard breaks",
			input: "Line one\\\nLine two",
			want:  "Line one\\\nLine two\n",
		},
		{
			name:  "preserves code blocks",
			input: "Text before\n\n```\ncode\nhere\n```\n\nText after",
			want:  "Text before\n\n```\ncode\nhere\n```\n\nText after\n",
		},
		{
			name:  "preserves emphasis",
			input: "This is *very\nimportant* text",
			want:  "This is *very important* text\n",
		},
	}

	p := markdown.Parser{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := p.Parse(tt.input)
			result := Unwrap(doc)
			got := markdown.Format(result)
			if got != tt.want {
				t.Errorf("Unwrap() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{
			name:  "short line unchanged",
			input: "Hello world",
			width: 80,
			want:  "Hello world\n",
		},
		{
			name:  "long line wrapped",
			input: "This is a very long line that should be wrapped at the specified width",
			width: 30,
			want:  "This is a very long line that\nshould be wrapped at the\nspecified width\n",
		},
		{
			name:  "preserves paragraphs",
			input: "First paragraph with some longer text here.\n\nSecond paragraph also with text.",
			width: 25,
			want:  "First paragraph with some\nlonger text here.\n\nSecond paragraph also\nwith text.\n",
		},
		{
			name:  "preserves code blocks",
			input: "Text\n\n```\nlong code line that should not be wrapped\n```",
			width: 20,
			want:  "Text\n\n```\nlong code line that should not be wrapped\n```\n",
		},
	}

	p := markdown.Parser{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := p.Parse(tt.input)
			result := Wrap(doc, tt.width)
			got := markdown.Format(result)
			if got != tt.want {
				t.Errorf("Wrap(%d) = %q, want %q", tt.width, got, tt.want)
			}
		})
	}
}

func TestWrapLists(t *testing.T) {
	input := "- This is a list item with a long description that should wrap\n- Second item"
	p := markdown.Parser{}
	doc := p.Parse(input)
	result := Wrap(doc, 30)
	got := markdown.Format(result)

	// Should wrap within list items but preserve list structure
	if !strings.Contains(got, "- This is a list item") {
		t.Errorf("List structure not preserved: %q", got)
	}
	if !strings.Contains(got, "- Second item") {
		t.Errorf("Second list item missing: %q", got)
	}
}

func TestRoundTrip(t *testing.T) {
	// Test that unwrap(wrap(text)) preserves meaning
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "simple paragraph",
			input: "This is a simple paragraph with some text.",
		},
		{
			name:  "with emphasis",
			input: "This has *emphasis* and **strong** text.",
		},
		{
			name:  "with links",
			input: "Check out [this link](https://example.com) for more.",
		},
		{
			name:  "multiple paragraphs",
			input: "First paragraph.\n\nSecond paragraph.",
		},
	}

	p := markdown.Parser{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := p.Parse(tt.input)

			// Wrap then unwrap
			wrapped := Wrap(doc, 20)
			unwrapped := Unwrap(wrapped)

			// Compare unwrapped text with original (after normalizing)
			original := Unwrap(p.Parse(tt.input))
			got := markdown.Format(unwrapped)
			want := markdown.Format(original)

			if got != want {
				t.Errorf("Round trip failed:\n  got:  %q\n  want: %q", got, want)
			}
		})
	}
}
