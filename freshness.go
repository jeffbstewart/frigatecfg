package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Freshness is what Frigate's own API says about recording and
// detection progress: the signal the 2026-11-03 NAS reboot showed was
// missing (curtilage docs/RESILIENCE.md, "Freshness metrics and the
// page").  The API, not the SQLite file: the sidecar runs as nobody on
// a read-only mount of a root-owned WAL database, and the API answered
// correctly throughout the incident (stale data, honestly reported).
type Freshness struct {
	// NewestSegment is the end_time of the newest recording segment
	// on any camera; zero when no camera has one in the window asked.
	NewestSegment time.Time
	// NewestEvent is the start_time of the newest event; zero when
	// Frigate has none.
	NewestEvent time.Time
	// StaleOpenReviews counts review items still open (no end_time)
	// after staleAfter: the fingerprint of a detection pipeline that
	// stopped consuming.  Five items sat open for a day in the
	// incident.
	StaleOpenReviews int
}

// frigateAPI is the pod-local internal port (5000: unauthenticated,
// the one Prometheus scrapes).
type frigateAPI struct {
	base   string
	client *http.Client
}

func newFrigateAPI(base string) *frigateAPI {
	return &frigateAPI{base: base, client: &http.Client{Timeout: 30 * time.Second}}
}

// segmentWindow is how far back the per-camera recordings query
// looks.  The endpoint's own defaults are evaluated once at import
// (stale), so the bounds are always explicit.
const segmentWindow = time.Hour

// reviewLimit bounds the review listing; open items are returned
// whatever the time window, so the limit is the only cap.
const reviewLimit = 1000

// get decodes one JSON endpoint.  A non-200 is an error: the gauges
// keep their last value and the check-errors counter moves.
func (a *frigateAPI) get(ctx context.Context, path string, q url.Values, into any) error {
	u := a.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}

// freshness asks the three questions.  Any failed call fails the
// whole read; a partial answer would make a dead camera look fresh.
func (a *frigateAPI) freshness(ctx context.Context, now time.Time, staleAfter time.Duration) (Freshness, error) {
	var f Freshness

	// Cameras from the running config, so a camera retired in git is
	// not asked about.
	var cfg struct {
		Cameras map[string]json.RawMessage `json:"cameras"`
	}
	if err := a.get(ctx, "/api/config", nil, &cfg); err != nil {
		return f, err
	}
	for name := range cfg.Cameras {
		var segs []struct {
			EndTime float64 `json:"end_time"`
		}
		q := url.Values{}
		q.Set("after", fmt.Sprintf("%d", now.Add(-segmentWindow).Unix()))
		q.Set("before", fmt.Sprintf("%d", now.Add(time.Minute).Unix()))
		if err := a.get(ctx, "/api/"+url.PathEscape(name)+"/recordings", q, &segs); err != nil {
			return f, err
		}
		for _, s := range segs {
			if t := unixFloat(s.EndTime); t.After(f.NewestSegment) {
				f.NewestSegment = t
			}
		}
	}

	// Events: newest first by default.
	var events []struct {
		StartTime float64 `json:"start_time"`
	}
	q := url.Values{}
	q.Set("limit", "1")
	if err := a.get(ctx, "/api/events", q, &events); err != nil {
		return f, err
	}
	if len(events) > 0 {
		f.NewestEvent = unixFloat(events[0].StartTime)
	}

	// Review items: open ones (end_time null) are returned regardless
	// of the time window.
	var reviews []struct {
		StartTime float64  `json:"start_time"`
		EndTime   *float64 `json:"end_time"`
	}
	q = url.Values{}
	q.Set("limit", fmt.Sprintf("%d", reviewLimit))
	if err := a.get(ctx, "/api/review", q, &reviews); err != nil {
		return f, err
	}
	for _, r := range reviews {
		if r.EndTime == nil && now.Sub(unixFloat(r.StartTime)) > staleAfter {
			f.StaleOpenReviews++
		}
	}
	return f, nil
}

// unixFloat is Frigate's timestamp convention: seconds, fractional.
func unixFloat(s float64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	sec := int64(s)
	return time.Unix(sec, int64((s-float64(sec))*1e9))
}
