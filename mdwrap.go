package main

import (
	"strings"

	"rsc.io/markdown"
)

// Unwrap transforms a markdown AST to join soft-wrapped lines.
// SoftBreaks become spaces, and newlines within Plain text become spaces.
// This is the inverse of Wrap and is used when sending comments to GitHub.
func Unwrap(b markdown.Block) markdown.Block {
	return walkBlock(b, unwrapInlines)
}

// Wrap transforms a markdown AST to wrap text at the given width.
// This is used when receiving comments from GitHub to make them readable in an editor.
func Wrap(b markdown.Block, width int) markdown.Block {
	return walkBlock(b, func(inlines markdown.Inlines) markdown.Inlines {
		return wrapInlines(inlines, width)
	})
}

// walkBlock recursively walks a block, applying fn to any Inlines it contains.
func walkBlock(b markdown.Block, fn func(markdown.Inlines) markdown.Inlines) markdown.Block {
	switch b := b.(type) {
	case *markdown.Document:
		for i, child := range b.Blocks {
			b.Blocks[i] = walkBlock(child, fn)
		}
	case *markdown.Paragraph:
		b.Text.Inline = fn(b.Text.Inline)
	case *markdown.Heading:
		b.Text.Inline = fn(b.Text.Inline)
	case *markdown.Quote:
		for i, child := range b.Blocks {
			b.Blocks[i] = walkBlock(child, fn)
		}
	case *markdown.List:
		for i, item := range b.Items {
			b.Items[i] = walkBlock(item, fn)
		}
	case *markdown.Item:
		for i, child := range b.Blocks {
			b.Blocks[i] = walkBlock(child, fn)
		}
	case *markdown.Text:
		b.Inline = fn(b.Inline)
	// CodeBlock, HTMLBlock, ThematicBreak, Empty - no inlines to process
	}
	return b
}

// unwrapInlines replaces SoftBreaks with spaces and joins newlines in Plain text.
func unwrapInlines(inlines markdown.Inlines) markdown.Inlines {
	result := make(markdown.Inlines, 0, len(inlines))
	for _, inl := range inlines {
		switch inl := inl.(type) {
		case *markdown.SoftBreak:
			// Replace soft break with space
			result = append(result, &markdown.Plain{Text: " "})
		case *markdown.Plain:
			// Replace newlines with spaces
			text := strings.ReplaceAll(inl.Text, "\n", " ")
			result = append(result, &markdown.Plain{Text: text})
		case *markdown.Strong:
			inl.Inner = unwrapInlines(inl.Inner)
			result = append(result, inl)
		case *markdown.Emph:
			inl.Inner = unwrapInlines(inl.Inner)
			result = append(result, inl)
		case *markdown.Del:
			inl.Inner = unwrapInlines(inl.Inner)
			result = append(result, inl)
		case *markdown.Link:
			inl.Inner = unwrapInlines(inl.Inner)
			result = append(result, inl)
		case *markdown.Image:
			inl.Inner = unwrapInlines(inl.Inner)
			result = append(result, inl)
		default:
			// HardBreak, Code, AutoLink, HTMLTag, Emoji, Escaped - keep as-is
			result = append(result, inl)
		}
	}
	return mergePlain(result)
}

// wrapInlines wraps text at the given width by inserting SoftBreaks.
func wrapInlines(inlines markdown.Inlines, width int) markdown.Inlines {
	// First unwrap to normalize, then re-wrap
	inlines = unwrapInlines(inlines)

	result := make(markdown.Inlines, 0, len(inlines))
	pos := 0 // current position in line

	for _, inl := range inlines {
		switch inl := inl.(type) {
		case *markdown.Plain:
			wrapped, newPos := wrapPlain(inl.Text, width, pos)
			result = append(result, wrapped...)
			pos = newPos
		case *markdown.Strong:
			// Account for ** markers
			inl.Inner = wrapInlinesAt(inl.Inner, width, pos+2)
			result = append(result, inl)
			pos += inlineLen(inl)
		case *markdown.Emph:
			// Account for * marker
			inl.Inner = wrapInlinesAt(inl.Inner, width, pos+1)
			result = append(result, inl)
			pos += inlineLen(inl)
		case *markdown.Del:
			inl.Inner = wrapInlinesAt(inl.Inner, width, pos+2)
			result = append(result, inl)
			pos += inlineLen(inl)
		case *markdown.Link:
			inl.Inner = wrapInlinesAt(inl.Inner, width, pos+1)
			result = append(result, inl)
			pos += inlineLen(inl)
		case *markdown.Image:
			inl.Inner = wrapInlinesAt(inl.Inner, width, pos+2)
			result = append(result, inl)
			pos += inlineLen(inl)
		case *markdown.Code:
			result = append(result, inl)
			pos += len(inl.Text) + 2 // backticks
		case *markdown.HardBreak:
			result = append(result, inl)
			pos = 0
		default:
			result = append(result, inl)
			pos += inlineLen(inl)
		}
	}
	return result
}

// wrapInlinesAt wraps inlines starting at a given position.
func wrapInlinesAt(inlines markdown.Inlines, width, startPos int) markdown.Inlines {
	// For nested elements, we do a simplified wrap
	// This could be improved but handles most cases
	result := make(markdown.Inlines, 0, len(inlines))
	pos := startPos

	for _, inl := range inlines {
		switch inl := inl.(type) {
		case *markdown.Plain:
			wrapped, newPos := wrapPlain(inl.Text, width, pos)
			result = append(result, wrapped...)
			pos = newPos
		default:
			result = append(result, inl)
			pos += inlineLen(inl)
		}
	}
	return result
}

// wrapPlain wraps plain text, returning the resulting inlines and final position.
func wrapPlain(text string, width, pos int) (markdown.Inlines, int) {
	if width <= 0 || text == "" {
		return markdown.Inlines{&markdown.Plain{Text: text}}, pos + len(text)
	}

	var result markdown.Inlines
	var current strings.Builder

	// Preserve leading space
	hasLeadingSpace := len(text) > 0 && (text[0] == ' ' || text[0] == '\t')
	hasTrailingSpace := len(text) > 0 && (text[len(text)-1] == ' ' || text[len(text)-1] == '\t')

	words := strings.Fields(text)
	if len(words) == 0 {
		// Text is all whitespace
		return markdown.Inlines{&markdown.Plain{Text: text}}, pos + len(text)
	}

	for i, word := range words {
		wordLen := len(word)

		// Need space before this word?
		needSpace := (i > 0) || (hasLeadingSpace && pos > 0)

		// If this word would exceed width and we're not at start of line
		if pos > 0 && pos+boolToInt(needSpace)+wordLen > width {
			// Emit current text and soft break
			if current.Len() > 0 {
				result = append(result, &markdown.Plain{Text: current.String()})
				current.Reset()
			}
			result = append(result, &markdown.SoftBreak{})
			pos = 0
			needSpace = false
		}

		// Add space before word if needed
		if needSpace && (current.Len() > 0 || pos > 0) {
			current.WriteByte(' ')
			pos++
		}

		current.WriteString(word)
		pos += wordLen
	}

	// Emit remaining text
	if current.Len() > 0 {
		s := current.String()
		if hasTrailingSpace {
			s += " "
			pos++
		}
		result = append(result, &markdown.Plain{Text: s})
	}

	return result, pos
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// inlineLen estimates the rendered length of an inline element.
func inlineLen(inl markdown.Inline) int {
	switch inl := inl.(type) {
	case *markdown.Plain:
		return len(inl.Text)
	case *markdown.Code:
		return len(inl.Text) + 2
	case *markdown.Strong:
		return inlinesLen(inl.Inner) + 4
	case *markdown.Emph:
		return inlinesLen(inl.Inner) + 2
	case *markdown.Del:
		return inlinesLen(inl.Inner) + 4
	case *markdown.Link:
		// [text](url)
		return inlinesLen(inl.Inner) + len(inl.URL) + 4
	case *markdown.Image:
		// ![text](url)
		return inlinesLen(inl.Inner) + len(inl.URL) + 5
	case *markdown.SoftBreak, *markdown.HardBreak:
		return 0
	case *markdown.Emoji:
		return len(inl.Text)
	case *markdown.AutoLink:
		return len(inl.URL)
	default:
		return 0
	}
}

// inlinesLen estimates the total rendered length of inline elements.
func inlinesLen(inlines markdown.Inlines) int {
	total := 0
	for _, inl := range inlines {
		total += inlineLen(inl)
	}
	return total
}

// mergePlain merges adjacent Plain elements.
func mergePlain(inlines markdown.Inlines) markdown.Inlines {
	if len(inlines) == 0 {
		return inlines
	}
	result := make(markdown.Inlines, 0, len(inlines))
	var pending *markdown.Plain

	for _, inl := range inlines {
		if p, ok := inl.(*markdown.Plain); ok {
			if pending == nil {
				pending = &markdown.Plain{Text: p.Text}
			} else {
				pending.Text += p.Text
			}
		} else {
			if pending != nil {
				result = append(result, pending)
				pending = nil
			}
			result = append(result, inl)
		}
	}
	if pending != nil {
		result = append(result, pending)
	}
	return result
}
