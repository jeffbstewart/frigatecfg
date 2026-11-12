package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeFrigate is the three endpoints freshness reads, scripted.
func fakeFrigate(t *testing.T, segments map[string][]float64, events []float64, reviews []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"cameras":{`)
		i := 0
		for name := range segments {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `%q:{"enabled":true}`, name)
			i++
		}
		fmt.Fprint(w, `},"mqtt":{"enabled":true}}`)
	})
	mux.HandleFunc("GET /api/{camera}/recordings", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" || r.URL.Query().Get("before") == "" {
			t.Errorf("recordings asked without explicit bounds: %s", r.URL)
		}
		fmt.Fprint(w, "[")
		for i, e := range segments[r.PathValue("camera")] {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":"s%d","start_time":%f,"end_time":%f,"segment_size":1.5,"motion":0,"objects":0,"duration":10}`, i, e-10, e)
		}
		fmt.Fprint(w, "]")
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "1" {
			t.Errorf("events asked without limit=1: %s", r.URL)
		}
		fmt.Fprint(w, "[")
		for i, s := range events {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":"e%d","camera":"front","label":"person","start_time":%f,"end_time":null}`, i, s)
		}
		fmt.Fprint(w, "]")
	})
	mux.HandleFunc("GET /api/review", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "[")
		for i, item := range reviews {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprint(w, item)
		}
		fmt.Fprint(w, "]")
	})
	return httptest.NewServer(mux)
}

func TestFreshnessReadsTheThreeSignals(t *testing.T) {
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) float64 { return float64(now.Add(d).UnixNano()) / 1e9 }
	srv := fakeFrigate(t,
		map[string][]float64{
			"front": {ts(-30 * time.Minute), ts(-20 * time.Minute)},
			"side":  {ts(-25 * time.Minute)},
			"dark":  {},
		},
		[]float64{ts(-2 * time.Hour)},
		[]string{
			// open for a day: the outage's fingerprint
			fmt.Sprintf(`{"id":"r1","camera":"front","start_time":%f,"end_time":null,"severity":"alert"}`, ts(-24*time.Hour)),
			// open, but only ten minutes: a real event in progress
			fmt.Sprintf(`{"id":"r2","camera":"porch","start_time":%f,"end_time":null,"severity":"detection"}`, ts(-10*time.Minute)),
			// closed long ago
			fmt.Sprintf(`{"id":"r3","camera":"porch","start_time":%f,"end_time":%f,"severity":"alert"}`, ts(-3*time.Hour), ts(-170*time.Minute)),
		})
	defer srv.Close()
	f, err := newFrigateAPI(srv.URL).freshness(context.Background(), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-20 * time.Minute); !f.NewestSegment.Equal(want) {
		t.Errorf("newest segment %v, want %v", f.NewestSegment, want)
	}
	if want := now.Add(-2 * time.Hour); !f.NewestEvent.Equal(want) {
		t.Errorf("newest event %v, want %v", f.NewestEvent, want)
	}
	if f.StaleOpenReviews != 1 {
		t.Errorf("stale open reviews %d, want 1", f.StaleOpenReviews)
	}
}

func TestFreshnessWithNothingRecordedIsZero(t *testing.T) {
	now := time.Now()
	srv := fakeFrigate(t, map[string][]float64{"front": {}}, nil, nil)
	defer srv.Close()
	f, err := newFrigateAPI(srv.URL).freshness(context.Background(), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !f.NewestSegment.IsZero() || !f.NewestEvent.IsZero() || f.StaleOpenReviews != 0 {
		t.Errorf("want all zero, got %+v", f)
	}
}

func TestFreshnessFailsWhole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			fmt.Fprint(w, `{"cameras":{"front":{}}}`)
			return
		}
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := newFrigateAPI(srv.URL).freshness(context.Background(), time.Now(), time.Hour); err == nil {
		t.Error("a failed camera call must fail the read")
	}
}
