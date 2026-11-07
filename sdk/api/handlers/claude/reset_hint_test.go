package claude

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// T3 Code resumes a limited thread by finding "(reset after 1h2m3s)" in the text Claude
// Code prints for a 429 on an API key. The CLI never reports a plan reset time for API-key
// traffic, so the Hub has to put it in the error message itself.
var t3ResetAfterPattern = regexp.MustCompile(`\(reset after (?:(\d+)h ?)?(?:(\d+)m ?)?(?:(\d+)s)?\)`)

func claudeErrorMessageFor(t *testing.T, status int, resetIn time.Duration) (message string, retryAfter string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	handler := &ClaudeCodeAPIHandler{}

	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: status,
		Error:      coreauth.NewModelCooldownError("claude-opus-5-5", "claude", resetIn),
	})

	return ojson.GetBytesNoCopy(recorder.Body.Bytes(), "error.message").String(), recorder.Header().Get("Retry-After")
}

func TestWriteClaudeErrorResponse_ResetHintForQuotaWait(t *testing.T) {
	tests := []struct {
		name    string
		resetIn time.Duration
		want    string
	}{
		{"wait of a few hours", 90 * time.Minute, "(reset after 1h30m0s)"},
		{"exactly the five minute threshold", 5 * time.Minute, "(reset after 5m0s)"},
		{"exactly five hours", 5 * time.Hour, "(reset after 5h0m0s)"},
		{"multi-day wait stays in hours", 30*time.Hour + 7*time.Minute + 9*time.Second, "(reset after 30h7m9s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, _ := claudeErrorMessageFor(t, http.StatusTooManyRequests, tt.resetIn)
			if !strings.HasSuffix(message, " "+tt.want) {
				t.Fatalf("message = %q, want suffix %q", message, " "+tt.want)
			}
			if !strings.Contains(message, "cooling down") {
				t.Fatalf("message = %q lost its original text", message)
			}
		})
	}
}

func TestWriteClaudeErrorResponse_NoResetHintForTransientOrNon429(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		resetIn time.Duration
	}{
		{"short cooldown is a transient throttle", http.StatusTooManyRequests, 20 * time.Second},
		{"just under the quota threshold", http.StatusTooManyRequests, 4*time.Minute + 59*time.Second},
		{"non-429 status", http.StatusServiceUnavailable, 90 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, _ := claudeErrorMessageFor(t, tt.status, tt.resetIn)
			if strings.Contains(message, "reset after") {
				t.Fatalf("message = %q, want no reset hint", message)
			}
		})
	}
}

// The hint must decode, with T3's own pattern, to the same wait the Retry-After header states.
func TestWriteClaudeErrorResponse_ResetHintAgreesWithRetryAfter(t *testing.T) {
	message, retryAfter := claudeErrorMessageFor(t, http.StatusTooManyRequests, 27*time.Hour+41*time.Minute+3*time.Second)

	match := t3ResetAfterPattern.FindStringSubmatch(message)
	if match == nil {
		t.Fatalf("T3 pattern found no reset hint in %q", message)
	}
	var seconds int
	for i, unit := range []int{3600, 60, 1} {
		if match[i+1] != "" {
			n, err := strconv.Atoi(match[i+1])
			if err != nil {
				t.Fatal(err)
			}
			seconds += n * unit
		}
	}
	want, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After = %q: %v", retryAfter, err)
	}
	if seconds != want {
		t.Fatalf("hint decodes to %ds, Retry-After says %ds (message %q)", seconds, want, message)
	}
}
