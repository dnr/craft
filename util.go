package main

import (
	"path/filepath"
	"strings"
)

func getIndentation(line string) string {
	for i, r := range line {
		if !(r == ' ' || r == '\t') {
			return line[:i]
		}
	}
	return line
}

func wrapText(text string, width int, indent string) []string {
	// Split by existing newlines first to preserve them
	paragraphs := strings.Split(text, "\n")
	var result []string

	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			// Preserve empty lines
			result = append(result, "")
			continue
		}

		if len(paragraph) <= width {
			result = append(result, paragraph)
			continue
		}

		// Word wrap this paragraph
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			result = append(result, paragraph)
			continue
		}

		currentLine := words[0]
		for _, word := range words[1:] {
			if len(currentLine)+len(word)+1 <= width {
				currentLine += " " + word
			} else {
				result = append(result, currentLine)
				currentLine = word
			}
		}

		if currentLine != "" {
			result = append(result, currentLine)
		}
	}

	return result
}

func createHorizontalRule(prefixLen int, headerText string, leadingDashes int) string {
	trailingDashCount := MaxLineLength - prefixLen - leadingDashes - len(headerText) - 2 // -2 for spaces
	if trailingDashCount < 3 {
		trailingDashCount = 3
	}

	return strings.Repeat(RuleChar, leadingDashes) + " " + headerText + " " + strings.Repeat(RuleChar, trailingDashCount)
}

func getLanguageCommentPrefix(filePath string) string {
	switch filepath.Ext(filePath) {
	case ".go", ".js", ".ts", ".tsx", ".jsx", ".c", ".cpp", ".h", ".hpp", ".java", ".rs", ".php":
		return "//"
	case ".py", ".sh", ".rb", ".yaml", ".yml":
		return "#"
	default:
		return "#" // Default fallback
	}
}
