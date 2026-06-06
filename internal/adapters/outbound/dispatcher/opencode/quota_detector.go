package opencode

import (
	"regexp"
	"strconv"
	"strings"
)

// transportTokens are strings that indicate an HTTP-level rate-limit or
// retry-exhaustion signal from an LLM provider. One of these MUST be
// present alongside a quota token for the co-occurrence guard to fire.
var transportTokens = []string{
	"429",
	"rate limit",
	"AI_RetryError",
	"maxRetriesExceeded",
}

// quotaTokens are strings that specifically indicate quota exhaustion (as
// opposed to a generic rate-limit or transient error). One of these MUST
// be present alongside a transport token for the co-occurrence guard to fire.
var quotaTokens = []string{
	"quota_exceeded",
	"quota exceeded",
	"x-ratelimit-exceeded",
}

// retryAfterSpecificRE matches the provider-specific header reflection:
//
//	x-ratelimit-quota-exceeded-retry-after: <seconds>
//	x-ratelimit-quota-exceeded-retry-after": "<seconds>"   (JSON-logged form)
//	x-ratelimit-quota-exceeded-retry-after=<seconds>
//	x-ratelimit-quota-exceeded-retry-after <seconds>
//
// The value is in seconds (integer). Quotes around the value are optional
// to handle both raw header strings and JSON-serialised response headers.
var retryAfterSpecificRE = regexp.MustCompile(
	`(?i)x-ratelimit-quota-exceeded-retry-after["']?\s*[:\s=]+["']?(\d+)`,
)

// retryAfterGenericRE matches the generic Retry-After header reflection:
//
//	retry-after: <seconds>
//	retry-after": "<seconds>"   (JSON-logged form)
//	retry-after=<seconds>
//	retry-after <seconds>
//
// The value is in seconds (integer). Used as fallback when the specific
// header is absent.
var retryAfterGenericRE = regexp.MustCompile(
	`(?i)\bretry-after["']?\s*[:\s=]+["']?(\d+)`,
)

// detectProviderQuota scans the combined stdout+stderr from a runtime
// receipt for quota-exhaustion signals.
//
// Co-occurrence guard: at least ONE transport token AND at least ONE quota
// token must be present in the combined text. This prevents a benign log
// line containing an isolated "429" substring from triggering a false
// positive.
//
// When both token classes are found, the function also parses the
// retry-after hint from the provider-specific header reflection first,
// then falls back to the generic Retry-After header. The returned
// retryAfterSeconds is 0 when no retry-after value is found.
//
// evidence is a short snippet (≤200 chars) of the matching region for
// logging and SSE payloads. ok is true only when quota is detected.
func detectProviderQuota(stdout, stderr string) (retryAfterSeconds int, evidence string, ok bool) {
	combined := stdout + "\n" + stderr

	hasTransport := false
	for _, tok := range transportTokens {
		if strings.Contains(combined, tok) {
			hasTransport = true
			break
		}
	}
	if !hasTransport {
		return 0, "", false
	}

	hasQuota := false
	for _, tok := range quotaTokens {
		if strings.Contains(combined, tok) {
			hasQuota = true
			break
		}
	}
	if !hasQuota {
		return 0, "", false
	}

	// Both token classes found — quota confirmed. Parse retry-after.
	retryAfterSeconds = parseRetryAfter(combined)
	evidence = extractEvidence(combined)
	return retryAfterSeconds, evidence, true
}

// parseRetryAfter extracts the retry-after hint in seconds from text.
// It prefers the provider-specific header (x-ratelimit-quota-exceeded-retry-after)
// and falls back to the generic Retry-After header. Returns 0 if neither is found.
func parseRetryAfter(text string) int {
	if m := retryAfterSpecificRE.FindStringSubmatch(text); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
			return v
		}
	}
	if m := retryAfterGenericRE.FindStringSubmatch(text); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

// extractEvidence returns a short snippet (≤200 chars) from the combined
// text for use in error messages and SSE payloads. It attempts to include
// the region around the first quota token match.
func extractEvidence(text string) string {
	const maxLen = 200

	// Find the position of the first quota token to anchor the snippet.
	idx := -1
	for _, tok := range quotaTokens {
		if i := strings.Index(text, tok); i >= 0 {
			if idx < 0 || i < idx {
				idx = i
			}
		}
	}

	var snippet string
	if idx >= 0 {
		start := idx - 80
		if start < 0 {
			start = 0
		}
		end := idx + 120
		if end > len(text) {
			end = len(text)
		}
		snippet = strings.TrimSpace(text[start:end])
	} else {
		snippet = strings.TrimSpace(text)
	}

	// Normalise whitespace: collapse runs of newlines/spaces.
	snippet = strings.Join(strings.Fields(snippet), " ")

	if len(snippet) > maxLen {
		snippet = snippet[:maxLen]
	}
	return snippet
}
