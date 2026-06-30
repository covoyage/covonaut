package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TruncationResult holds the outcome of a truncation operation.
type TruncationResult struct {
	Content          string `json:"content"`
	Truncated        bool   `json:"truncated"`
	TruncatedBy      string `json:"truncated_by,omitempty"` // "lines", "bytes", or ""
	TotalLines       int    `json:"total_lines"`
	TotalBytes       int    `json:"total_bytes"`
	OutputLines      int    `json:"output_lines"`
	OutputBytes      int    `json:"output_bytes"`
	LastLinePartial  bool   `json:"last_line_partial"`
	FirstLineExceeds bool   `json:"first_line_exceeds"`
	MaxLines         int    `json:"max_lines"`
	MaxBytes         int    `json:"max_bytes"`
}

// TruncationOptions configures truncation behavior.
type TruncationOptions struct {
	MaxLines int
	MaxBytes int
}

// FormatSize returns a human-readable byte size.
func FormatSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// TruncateHead keeps the first N lines/bytes. Never returns partial lines.
func TruncateHead(content string, opts TruncationOptions) TruncationResult {
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = 2000
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 50 * 1024
	}

	totalBytes := len([]byte(content))
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			Truncated:   false,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	firstLineBytes := len([]byte(lines[0]))
	if firstLineBytes > maxBytes {
		return TruncationResult{
			Content:          "",
			Truncated:        true,
			TruncatedBy:      "bytes",
			TotalLines:       totalLines,
			TotalBytes:       totalBytes,
			OutputLines:      0,
			OutputBytes:      0,
			FirstLineExceeds: true,
			MaxLines:         maxLines,
			MaxBytes:         maxBytes,
		}
	}

	var out []string
	outBytes := 0
	truncatedBy := "lines"

	for i, line := range lines {
		if i >= maxLines {
			truncatedBy = "lines"
			break
		}
		lineBytes := len([]byte(line))
		if i > 0 {
			lineBytes++ // newline
		}
		if outBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		out = append(out, line)
		outBytes += lineBytes
	}

	outContent := strings.Join(out, "\n")
	return TruncationResult{
		Content:     outContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(out),
		OutputBytes: len([]byte(outContent)),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

// TruncateTail keeps the last N lines/bytes. May return a partial first line.
func TruncateTail(content string, opts TruncationOptions) TruncationResult {
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = 2000
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 50 * 1024
	}

	totalBytes := len([]byte(content))
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			Truncated:   false,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	var out []string
	outBytes := 0
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		line := lines[i]
		lineBytes := len([]byte(line))
		if len(out) > 0 {
			lineBytes++ // newline
		}
		if outBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(out) == 0 {
				// Edge case: last line alone exceeds maxBytes.
				truncated := truncateStringToBytesFromEnd(line, maxBytes)
				out = append(out, truncated)
				outBytes = len([]byte(truncated))
				lastLinePartial = true
			}
			break
		}
		out = append([]string{line}, out...)
		outBytes += lineBytes
	}

	outContent := strings.Join(out, "\n")
	return TruncationResult{
		Content:         outContent,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(out),
		OutputBytes:     len([]byte(outContent)),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines,
		MaxBytes:        maxBytes,
	}
}

// truncateStringToBytesFromEnd truncates a string from the start to fit within maxBytes.
func truncateStringToBytesFromEnd(s string, maxBytes int) string {
	b := []byte(s)
	if len(b) <= maxBytes {
		return s
	}
	start := len(b) - maxBytes
	// Find valid UTF-8 boundary.
	for start < len(b) && (b[start]&0xc0) == 0x80 {
		start++
	}
	return string(b[start:])
}

// TruncateLine truncates a single line to maxChars, adding a suffix.
func TruncateLine(line string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = 500
	}
	runes := []rune(line)
	if len(runes) <= maxChars {
		return line, false
	}
	return string(runes[:maxChars]) + "... [truncated]", true
}

// CountRunes returns the number of runes in a string.
func CountRunes(s string) int {
	return utf8.RuneCountInString(s)
}
