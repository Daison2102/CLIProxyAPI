package auth

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestSoonestResetSelectsEarliestWeeklyWindow(t *testing.T) {
	now := time.Now()
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			later := resetTestAuth("a-later", provider, now.Add(96*time.Hour), now)
			earlier := resetTestAuth("z-earlier", provider, now.Add(24*time.Hour), now)
			selector := &SoonestResetSelector{}
			for range 3 {
				picked, err := selector.Pick(context.Background(), provider, "model", cliproxyexecutor.Options{}, []*Auth{later, earlier})
				if err != nil || picked.ID != earlier.ID {
					t.Fatalf("picked %v, error %v; want earliest weekly reset", picked, err)
				}
			}
		})
	}
}

func TestSoonestResetProbesUnknownOnceThenUsesKnownWindow(t *testing.T) {
	now := time.Now()
	known := resetTestAuth("known", "claude", now.Add(24*time.Hour), now)
	unknown := &Auth{ID: "unknown", Provider: "claude"}
	selector := &SoonestResetSelector{}
	for i, want := range []string{"unknown", "known", "known"} {
		picked, err := selector.Pick(context.Background(), "claude", "model", cliproxyexecutor.Options{}, []*Auth{known, unknown})
		if err != nil || picked.ID != want {
			t.Fatalf("pick %d: got %v, error %v; want %s", i, picked, err, want)
		}
	}
}

func TestSoonestResetPreservesPriorityAndAvailability(t *testing.T) {
	now := time.Now()
	early := resetTestAuth("early", "codex", now.Add(24*time.Hour), now)
	high := resetTestAuth("high", "codex", now.Add(96*time.Hour), now)
	high.Attributes = map[string]string{"priority": "2"}
	selector := &SoonestResetSelector{}
	picked, err := selector.Pick(context.Background(), "codex", "model", cliproxyexecutor.Options{}, []*Auth{early, high})
	if err != nil || picked.ID != "high" {
		t.Fatalf("priority ignored: %v, %v", picked, err)
	}
	high.Disabled = true
	picked, err = selector.Pick(context.Background(), "codex", "model", cliproxyexecutor.Options{}, []*Auth{early, high})
	if err != nil || picked.ID != "early" {
		t.Fatalf("disabled account selected: %v, %v", picked, err)
	}
	early.Disabled = true
	if _, err = selector.Pick(context.Background(), "codex", "model", cliproxyexecutor.Options{}, []*Auth{early, high}); err == nil {
		t.Fatal("expected an error when all accounts are disabled")
	}
}

func TestSoonestResetAffinitySurvivesQuotaOrderChange(t *testing.T) {
	now := time.Now()
	a := resetTestAuth("a", "claude", now.Add(24*time.Hour), now)
	b := resetTestAuth("b", "claude", now.Add(96*time.Hour), now)
	selector := NewSessionAffinitySelector(&SoonestResetSelector{})
	defer selector.Stop()
	opts := cliproxyexecutor.Options{Headers: http.Header{"X-Claude-Code-Session-Id": {"reset-routing-test"}}}
	pick := func(want string) {
		t.Helper()
		picked, err := selector.Pick(context.Background(), "claude", "model", opts, []*Auth{a, b})
		if err != nil || picked.ID != want {
			t.Fatalf("got %v, %v; want %s", picked, err, want)
		}
	}
	pick("a")
	b.Quota = resetTestAuth("b", "claude", now.Add(time.Hour), now).Quota
	pick("a")
	a.Disabled = true
	pick("b")
}

func TestSoonestResetIgnoresShortWindowsAndMalformedDates(t *testing.T) {
	now := time.Now()
	for _, value := range []string{"not-a-date", strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)} {
		a := resetTestAuth("a", "codex", now.Add(96*time.Hour), now)
		b := resetTestAuth("b", "codex", now.Add(24*time.Hour), now)
		a.Quota.Signals["X-Codex-Primary-Window-Minutes"] = "300"
		a.Quota.Signals["X-Codex-Primary-Reset-At"] = strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
		b.Quota.Signals["X-Codex-Secondary-Reset-At"] = value
		selector := &SoonestResetSelector{}
		// One ordinary discovery request for b, then the valid weekly window on a.
		for _, want := range []string{"b", "a"} {
			picked, err := selector.Pick(context.Background(), "codex", "model", cliproxyexecutor.Options{}, []*Auth{a, b})
			if err != nil || picked.ID != want {
				t.Fatalf("got %v, %v; want %s", picked, err, want)
			}
		}
	}
}

func resetTestAuth(id, provider string, reset, now time.Time) *Auth {
	header := "Anthropic-Ratelimit-Unified-7d-Reset"
	headers := make(http.Header)
	if provider == "codex" {
		header = "X-Codex-Secondary-Reset-At"
		headers.Set("X-Codex-Secondary-Window-Minutes", "10080")
	}
	headers.Set(header, strconv.FormatInt(reset.Unix(), 10))
	a := &Auth{ID: id, Provider: provider}
	a.Quota.ObserveResponseHeadersForProvider(provider, headers, now)
	return a
}
