package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// A Codex client turns X-Codex-*-Used-Percent / Reset-At on a usage-limit rejection into the
// rate-limit snapshot that T3 Code schedules an automatic resume from. Behind the hub the client has
// no ChatGPT account to read it from, so the hub has to send it with the rejection itself.

func codexQuotaRejection(status int, wait time.Duration) *interfaces.ErrorMessage {
	return &interfaces.ErrorMessage{
		StatusCode: status,
		Error:      coreauth.NewModelCooldownError("gpt-6.1-sol", "codex", wait),
	}
}

func websocketErrorHeaders(t *testing.T, msg *interfaces.ErrorMessage) map[string]string {
	t.Helper()
	payload, err := buildResponsesWebsocketErrorPayload(msg)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	var event struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return event.Headers
}

func httpErrorHeaders(t *testing.T, h *OpenAIResponsesAPIHandler, msg *interfaces.ErrorMessage) http.Header {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	h.writeResponsesError(c, msg)
	if recorder.Code != msg.StatusCode {
		t.Fatalf("status = %d, want %d", recorder.Code, msg.StatusCode)
	}
	return recorder.Result().Header
}

func newResponsesHandler(cfg *sdkconfig.SDKConfig) *OpenAIResponsesAPIHandler {
	return NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(cfg, nil))
}

type codexQuotaCase struct {
	name       string
	status     int
	wait       time.Duration
	wantWindow string // "Primary", "Secondary", or "" for no quota headers
	wantMins   string
}

var codexQuotaCases = []codexQuotaCase{
	{"weekly-length wait reports the secondary window", http.StatusTooManyRequests, 30*time.Hour + 7*time.Minute + 9*time.Second, "Secondary", "10080"},
	{"just over five hours reports the secondary window", http.StatusTooManyRequests, 5*time.Hour + time.Second, "Secondary", "10080"},
	{"exactly five hours reports the primary window", http.StatusTooManyRequests, 5 * time.Hour, "Primary", "300"},
	{"wait of a few hours reports the primary window", http.StatusTooManyRequests, 90 * time.Minute, "Primary", "300"},
	{"exactly the five minute threshold is reported", http.StatusTooManyRequests, 5 * time.Minute, "Primary", "300"},
	{"short cooldown is a transient throttle", http.StatusTooManyRequests, 20 * time.Second, "", ""},
	{"just under the threshold is a transient throttle", http.StatusTooManyRequests, 4*time.Minute + 59*time.Second, "", ""},
	{"non-429 status is never reported", http.StatusBadRequest, 90 * time.Minute, "", ""},
}

// checkCodexQuota verifies a header lookup against one case.
func checkCodexQuota(t *testing.T, tc codexQuotaCase, get func(name string) string, before, after int64) {
	t.Helper()
	names := func(window string) []string {
		return []string{"X-Codex-" + window + "-Used-Percent", "X-Codex-" + window + "-Window-Minutes",
			"X-Codex-" + window + "-Reset-After-Seconds", "X-Codex-" + window + "-Reset-At"}
	}
	for _, window := range []string{"Primary", "Secondary"} {
		if window == tc.wantWindow {
			continue
		}
		for _, name := range names(window) {
			if got := get(name); got != "" {
				t.Fatalf("%s = %q, want absent", name, got)
			}
		}
	}
	if tc.wantWindow == "" {
		return
	}
	prefix := "X-Codex-" + tc.wantWindow + "-"
	if got := get(prefix + "Used-Percent"); got != "100" {
		t.Fatalf("%sUsed-Percent = %q, want 100", prefix, got)
	}
	if got := get(prefix + "Window-Minutes"); got != tc.wantMins {
		t.Fatalf("%sWindow-Minutes = %q, want %s", prefix, got, tc.wantMins)
	}
	wait := int64(tc.wait / time.Second)
	if got := get(prefix + "Reset-After-Seconds"); got != strconv.FormatInt(wait, 10) {
		t.Fatalf("%sReset-After-Seconds = %q, want %d", prefix, got, wait)
	}
	resetAt, err := strconv.ParseInt(get(prefix+"Reset-At"), 10, 64)
	if err != nil {
		t.Fatalf("%sReset-At = %q is not epoch seconds: %v", prefix, get(prefix+"Reset-At"), err)
	}
	if resetAt < before+wait || resetAt > after+wait+1 {
		t.Fatalf("%sReset-At = %d, want within [%d, %d]", prefix, resetAt, before+wait, after+wait+1)
	}
}

func TestResponsesWebsocketError_CodexQuotaHeaders(t *testing.T) {
	for _, tc := range codexQuotaCases {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now().Unix()
			headers := websocketErrorHeaders(t, codexQuotaRejection(tc.status, tc.wait))
			after := time.Now().Unix()
			checkCodexQuota(t, tc, func(name string) string { return headers[name] }, before, after)
		})
	}
}

func TestResponsesHTTPError_CodexQuotaHeaders(t *testing.T) {
	for _, tc := range codexQuotaCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newResponsesHandler(&sdkconfig.SDKConfig{})
			before := time.Now().Unix()
			header := httpErrorHeaders(t, h, codexQuotaRejection(tc.status, tc.wait))
			after := time.Now().Unix()
			checkCodexQuota(t, tc, header.Get, before, after)
			if tc.status == http.StatusTooManyRequests && tc.wantWindow != "" {
				if got := header.Get("Retry-After"); got != header.Get("X-Codex-"+tc.wantWindow+"-Reset-After-Seconds") {
					t.Fatalf("Retry-After = %q disagrees with the quota headers", got)
				}
			}
		})
	}
}

// Real upstream quota values, when the hub carries them, are more accurate than an estimate.
func TestResponsesWebsocketError_KeepsUpstreamCodexQuotaHeaders(t *testing.T) {
	msg := codexQuotaRejection(http.StatusTooManyRequests, 90*time.Minute)
	msg.Addon = http.Header{
		"X-Codex-Secondary-Used-Percent":   {"77"},
		"X-Codex-Secondary-Window-Minutes": {"10080"},
		"X-Codex-Secondary-Reset-At":       {"1999999999"},
	}

	headers := websocketErrorHeaders(t, msg)

	if headers["X-Codex-Secondary-Used-Percent"] != "77" || headers["X-Codex-Secondary-Reset-At"] != "1999999999" {
		t.Fatalf("upstream values were replaced: %v", headers)
	}
	for name := range headers {
		if strings.HasPrefix(name, "X-Codex-Primary-") {
			t.Fatalf("estimated %s was added next to upstream values", name)
		}
	}
}

func TestResponsesHTTPError_PassthroughUpstreamCodexQuotaHeadersWin(t *testing.T) {
	h := newResponsesHandler(&sdkconfig.SDKConfig{PassthroughHeaders: true})
	msg := codexQuotaRejection(http.StatusTooManyRequests, 90*time.Minute)
	msg.Addon = http.Header{
		"X-Codex-Secondary-Used-Percent":   {"77"},
		"X-Codex-Secondary-Window-Minutes": {"10080"},
		"X-Codex-Secondary-Reset-At":       {"1999999999"},
	}

	header := httpErrorHeaders(t, h, msg)

	if got := header.Values("X-Codex-Secondary-Used-Percent"); len(got) != 1 || got[0] != "77" {
		t.Fatalf("Secondary-Used-Percent = %v, want the upstream value 77", got)
	}
	if got := header.Values("X-Codex-Primary-Used-Percent"); len(got) != 0 {
		t.Fatalf("estimated Primary-Used-Percent %v was added next to upstream values", got)
	}
}

// Chat completions, images and the other OpenAI-format endpoints share the base writer and must not change.
func TestBaseErrorWriter_DoesNotAddCodexQuotaHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil).WriteErrorResponse(c, codexQuotaRejection(http.StatusTooManyRequests, 90*time.Minute))

	for name := range recorder.Result().Header {
		if strings.HasPrefix(name, "X-Codex-") {
			t.Fatalf("base writer added %s", name)
		}
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("expected the existing Retry-After to be unchanged")
	}
}
