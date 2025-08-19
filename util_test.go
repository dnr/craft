package main

import (
	"strings"
	"testing"
)

func TestCreateHorizontalRule(t *testing.T) {
	headerText := "reviewer ─ 2024-01-15 14:30"
	rule := createHorizontalRule(10, headerText, 5)

	// Should start with 5 dashes
	if !strings.HasPrefix(rule, strings.Repeat(RuleChar, 5)) {
		t.Error("Should start with 5 rule characters")
	}

	// Should contain the header text
	if !strings.Contains(rule, headerText) {
		t.Error("Should contain header text")
	}

	// Should end with dashes
	if !strings.HasSuffix(rule, RuleChar) {
		t.Error("Should end with rule characters")
	}

	// Should be a reasonable length (not empty, not too crazy long)
	if len(rule) < 20 || len(rule) > 300 {
		t.Errorf("Rule length should be reasonable, got %d chars", len(rule))
	}
}
