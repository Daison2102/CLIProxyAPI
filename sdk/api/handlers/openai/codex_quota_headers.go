package openai

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const (
	// A wait this long or longer is a usage window, not a transient throttle.
	// Shorter 429s are left alone so they do not turn into automatic resumes.
	codexQuotaMinRetryAfterSeconds = 5 * 60
	// The longest wait that still fits the five-hour session window.
	codexSessionWindowSeconds = 5 * 60 * 60
	codexSessionWindowMinutes = 300
	codexWeeklyWindowMinutes  = 10080
)

// codexQuotaResetHeaders describes an exhausted usage window the way the ChatGPT backend does.
//
// A Codex client builds its rate-limit snapshot from these X-Codex-* values and has no ChatGPT
// account to read it from behind the hub, so without them T3 Code has no reset time to schedule an
// automatic resume from. The wait comes from the Retry-After the hub already derives from the
// cooldown it is enforcing; the window is inferred from its length.
func codexQuotaResetHeaders(errMsg *interfaces.ErrorMessage, status int, nowUnix int64) http.Header {
	if errMsg == nil || errMsg.Error == nil || status != http.StatusTooManyRequests {
		return nil
	}
	retryAfter, err := strconv.ParseInt(strings.TrimSpace(coreauth.SafeResponseHeaders(errMsg.Error).Get("Retry-After")), 10, 64)
	if err != nil || retryAfter < codexQuotaMinRetryAfterSeconds {
		return nil
	}
	window, minutes := "Primary", codexSessionWindowMinutes
	if retryAfter > codexSessionWindowSeconds {
		window, minutes = "Secondary", codexWeeklyWindowMinutes
	}
	prefix := "X-Codex-" + window + "-"
	headers := make(http.Header)
	headers.Set(prefix+"Used-Percent", "100")
	headers.Set(prefix+"Window-Minutes", strconv.Itoa(minutes))
	headers.Set(prefix+"Reset-After-Seconds", strconv.FormatInt(retryAfter, 10))
	headers.Set(prefix+"Reset-At", strconv.FormatInt(nowUnix+retryAfter, 10))
	return headers
}

// hasCodexQuotaHeaders reports whether real upstream quota values are already present.
func hasCodexQuotaHeaders(headers http.Header) bool {
	for key := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if strings.HasPrefix(canonical, "X-Codex-Primary-") || strings.HasPrefix(canonical, "X-Codex-Secondary-") {
			return true
		}
	}
	return false
}

// writeResponsesError writes an error for the Responses API endpoints. It adds the Codex quota headers
// to a quota 429; other endpoints share the base writer and are left unchanged.
func (h *OpenAIResponsesAPIHandler) writeResponsesError(c *gin.Context, errMsg *interfaces.ErrorMessage) {
	if errMsg != nil && !errMsg.DirectResponse {
		status := http.StatusInternalServerError
		if errMsg.StatusCode > 0 {
			status = errMsg.StatusCode
		}
		// Upstream values, when passed through, are more accurate than this estimate.
		upstream := handlers.PassthroughHeadersEnabled(h.CurrentConfig()) && hasCodexQuotaHeaders(errMsg.Addon)
		if !upstream {
			for key, values := range codexQuotaResetHeaders(errMsg, status, time.Now().Unix()) {
				c.Writer.Header()[key] = values
			}
		}
	}
	h.WriteErrorResponse(c, errMsg)
}
