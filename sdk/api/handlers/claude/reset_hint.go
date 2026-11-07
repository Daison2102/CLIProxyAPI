package claude

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A wait this long or longer is a subscription usage window, not a transient throttle.
// Shorter 429s are left alone so they do not turn into automatic resumes.
const claudeQuotaMinRetryAfter = 5 * time.Minute

// withClaudeResetHint appends "(reset after 1h2m3s)" to the message of a quota 429.
//
// Claude Code reports a 429 on an API key as plain text and never attaches the plan
// reset time that a subscription limit carries, so T3 Code has nothing to schedule an
// automatic resume from. T3 reads this exact suffix from that text. The wait comes from
// Retry-After, which the Hub already derives from the cooldown it is enforcing.
func withClaudeResetHint(message string, status int, retryAfter string) string {
	if status != http.StatusTooManyRequests {
		return message
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(retryAfter), 10, 64)
	if err != nil || seconds < int64(claudeQuotaMinRetryAfter/time.Second) {
		return message
	}
	return message + " (reset after " + (time.Duration(seconds) * time.Second).String() + ")"
}
