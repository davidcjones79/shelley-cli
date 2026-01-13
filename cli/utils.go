package cli

import (
	"fmt"
	"strings"
)

// formatTokens formats token counts with K/M suffixes
func formatTokens(tokens uint64) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// formatTokensInt formats int token counts with K/M suffixes
func formatTokensInt(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// sanitizeSlug cleans a slug: lowercase, alphanumeric and hyphens only, max 60 chars
func sanitizeSlug(input string) string {
	result := strings.ToLower(input)
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "_", "-")

	var sb strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	result = sb.String()

	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	result = strings.Trim(result, "-")

	if len(result) > 60 {
		result = result[:60]
		result = strings.TrimSuffix(result, "-")
	}
	return result
}
