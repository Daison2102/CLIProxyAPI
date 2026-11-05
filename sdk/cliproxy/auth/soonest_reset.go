package auth

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// SoonestResetSelector spends the earliest-resetting weekly allowance first.
// SessionAffinitySelector wraps it so existing conversations keep their account.
type SoonestResetSelector struct {
	mu       sync.Mutex
	probed   map[string]time.Time
	fallback RoundRobinSelector
}

func (s *SoonestResetSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.probed == nil {
		s.probed = make(map[string]time.Time)
	}
	// Discovery uses ordinary client requests, never background inference. Keep
	// the record bounded, and allow an unknown/reset account a new probe hourly.
	for id, at := range s.probed {
		if now.Sub(at) >= time.Hour {
			delete(s.probed, id)
		}
	}
	if len(s.probed) >= 4096 {
		clear(s.probed)
	}
	var earliest time.Time
	var candidates []*Auth
	for _, a := range available {
		reset := weeklyResetAt(a, now)
		if reset.IsZero() {
			key := provider + "::" + a.ID
			if _, seen := s.probed[key]; !seen {
				s.probed[key] = now
				return a, nil
			}
			continue
		}
		if earliest.IsZero() || reset.Before(earliest) {
			earliest = reset
			candidates = []*Auth{a}
		} else if reset.Equal(earliest) {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		candidates = available
	}
	return s.fallback.Pick(ctx, provider, model, opts, candidates)
}

func weeklyResetAt(a *Auth, now time.Time) time.Time {
	signals := make(map[string]string, len(a.Quota.Signals))
	for key, value := range a.Quota.Signals {
		signals[strings.ToLower(key)] = value
	}
	parse := func(value string) time.Time {
		if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
			return time.Unix(seconds, 0)
		}
		valueTime, _ := time.Parse(time.RFC3339, value)
		return valueTime
	}
	var reset time.Time
	switch strings.ToLower(a.Provider) {
	case "claude":
		reset = parse(signals["anthropic-ratelimit-unified-7d-reset"])
	case "codex":
		for _, window := range []string{"primary", "secondary"} {
			prefix := "x-codex-" + window
			minutes, _ := strconv.ParseFloat(signals[prefix+"-window-minutes"], 64)
			if minutes < 24*60 && !(minutes == 0 && window == "secondary") {
				continue
			}
			candidate := parse(signals[prefix+"-reset-at"])
			if candidate.IsZero() && !a.Quota.ObservedAt.IsZero() {
				if seconds, err := strconv.ParseInt(signals[prefix+"-reset-after-seconds"], 10, 64); err == nil && seconds > 0 && seconds <= 30*24*60*60 {
					candidate = a.Quota.ObservedAt.Add(time.Duration(seconds) * time.Second)
				}
			}
			if candidate.After(now) && (reset.IsZero() || candidate.Before(reset)) {
				reset = candidate
			}
		}
	}
	if !reset.After(now) {
		return time.Time{}
	}
	return reset
}
