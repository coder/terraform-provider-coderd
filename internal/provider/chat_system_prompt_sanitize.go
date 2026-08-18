package provider

import (
	"strings"
	"unicode"
)

// sanitizePromptText mirrors coderd's chatd.SanitizePromptText
// (coderd/x/chatd/sanitize.go in coder/coder): it strips invisible
// Unicode characters, normalizes line endings, collapses excessive
// blank lines, and trims surrounding whitespace.
//
// The chat system prompt endpoint stores the sanitized form of
// whatever is PUT to it, so the value read back rarely matches the
// configured value byte-for-byte (a trailing newline from
// `file("system-prompt.md")` is the everyday case). This local copy
// exists so `system_prompt` can compare semantically: two values are
// the same setting iff they sanitize to the same string. The logic is
// deliberately a straight port; if the upstream sanitizer changes, the
// worst case is a visible (loud) diff on the next plan rather than
// silent drift.
func sanitizePromptText(s string) string {
	// 1. Normalize line endings.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	// 2. Strip invisible characters rune-by-rune.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !isVisiblePromptRune(r) {
			continue
		}
		_, _ = b.WriteRune(r)
	}
	s = b.String()

	// 3. Collapse 3+ consecutive newlines down to 2.
	s = collapsePromptNewlines(s)

	// 4. Final trim.
	return strings.TrimSpace(s)
}

// isVisiblePromptRune reports whether r survives coderd's prompt
// sanitization. The codepoint list matches chatd.isVisible upstream:
// an explicit list rather than blanket unicode.Cf stripping, so
// legitimate format characters (e.g. subdivision flag emoji) survive.
func isVisiblePromptRune(r rune) bool {
	switch {
	// Soft hyphen.
	case r == 0x00AD:
		return false
	// Combining grapheme joiner.
	case r == 0x034F:
		return false
	// Arabic letter mark.
	case r == 0x061C:
		return false
	// Mongolian vowel separator.
	case r == 0x180E:
		return false
	// Zero-width space.
	case r == 0x200B:
		return false
	// U+200C (ZWNJ) is deliberately NOT stripped, matching upstream:
	// it is required for correct rendering of Persian, Urdu, and
	// Kurdish scripts.
	// Zero-width joiner.
	case r == 0x200D:
		return false
	// Left-to-right and right-to-left marks.
	case r == 0x200E, r == 0x200F:
		return false
	// Bidi embedding and override controls: LRE, RLE, PDF, LRO, RLO.
	case r >= 0x202A && r <= 0x202E:
		return false
	// Word joiner and invisible operators.
	case r >= 0x2060 && r <= 0x2064:
		return false
	// Bidi isolate controls: LRI, RLI, FSI, PDI.
	case r >= 0x2066 && r <= 0x2069:
		return false
	// Deprecated format characters.
	case r >= 0x206A && r <= 0x206F:
		return false
	// Byte order mark / zero-width no-break space.
	case r == 0xFEFF:
		return false
	// Interlinear annotation anchor, separator, and terminator.
	case r >= 0xFFF9 && r <= 0xFFFB:
		return false
	default:
		return true
	}
}

// collapsePromptNewlines trims trailing whitespace from each line,
// then replaces runs of 3 or more consecutive newlines with exactly 2,
// matching chatd.collapseNewlines upstream.
func collapsePromptNewlines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	s = strings.Join(lines, "\n")

	var b strings.Builder
	b.Grow(len(s))
	consecutiveNewlines := 0
	for _, r := range s {
		if r == '\n' {
			consecutiveNewlines++
			if consecutiveNewlines <= 2 {
				_, _ = b.WriteRune(r)
			}
			continue
		}
		consecutiveNewlines = 0
		_, _ = b.WriteRune(r)
	}
	return b.String()
}
