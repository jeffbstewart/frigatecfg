package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// cmdWatch re-runs diff on an interval and serves the result as
// Prometheus gauges, for a sidecar beside Frigate (the live file sits
// on an RWO volume only that pod can mount):
//
//	frigatecfg_config_drift{kind="tuning"}      number of owned paths differing (pull pending)
//	frigatecfg_config_drift{kind="structural"}  number of structural paths differing (will revert)
//	frigatecfg_config_check_timestamp_seconds   last successful check
//	frigatecfg_config_check_errors_total        checks that failed to read/parse
//
// Drifting paths are logged on every change so `kubectl logs` shows
// what to pull or move.
func cmdWatch(args []string) error {
	fs := newFlags("watch")
	canonical := fs.String("canonical", "", "canonical (git-owned) config file")
	tuning := fs.String("tuning", "", "tuning (UI-owned paths) file; may be empty or absent")
	live := fs.String("live", "", "the live config.yml")
	interval := fs.Duration("interval", 5*time.Minute, "how often to re-check")
	listen := fs.String("listen", ":9117", "address to serve /metrics on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := required(fs, map[string]*string{"canonical": canonical, "tuning": tuning, "live": live}); err != nil {
		return err
	}

	var mu sync.Mutex
	var tuningN, structuralN int
	var lastOK time.Time
	var errorsN int
	lastReport := ""

	check := func() {
		c, t, err := readCanonicalTuning(*canonical, *tuning)
		var drifts []Drift
		if err == nil {
			ln, lerr := readDoc(*live)
			if errors.Is(lerr, os.ErrNotExist) {
				ln = emptyDoc()
			} else if lerr != nil {
				err = lerr
			}
			if err == nil {
				drifts, err = diff(c, t, ln)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errorsN++
			log.Printf("check failed: %v", err)
			return
		}
		tn, sn := 0, 0
		report := ""
		for _, d := range drifts {
			kind := "tuning"
			if d.Owned {
				tn++
			} else {
				sn++
				kind = "STRUCTURAL"
			}
			report += fmt.Sprintf("%s %s (live: %s; git: %s)\n", kind, d.Path, d.Live, d.Git)
		}
		tuningN, structuralN, lastOK = tn, sn, time.Now()
		if report != lastReport {
			if report == "" {
				log.Print("no drift")
			} else {
				log.Print("drift:\n" + report)
			}
			lastReport = report
		}
	}

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# HELP frigatecfg_config_drift Paths whose live value differs from git, by kind.\n")
		fmt.Fprintf(w, "# TYPE frigatecfg_config_drift gauge\n")
		fmt.Fprintf(w, "frigatecfg_config_drift{kind=\"tuning\"} %d\n", tuningN)
		fmt.Fprintf(w, "frigatecfg_config_drift{kind=\"structural\"} %d\n", structuralN)
		fmt.Fprintf(w, "# HELP frigatecfg_config_check_timestamp_seconds Unix time of the last successful check.\n")
		fmt.Fprintf(w, "# TYPE frigatecfg_config_check_timestamp_seconds gauge\n")
		fmt.Fprintf(w, "frigatecfg_config_check_timestamp_seconds %d\n", lastOK.Unix())
		fmt.Fprintf(w, "# HELP frigatecfg_config_check_errors_total Checks that failed to read or parse a file.\n")
		fmt.Fprintf(w, "# TYPE frigatecfg_config_check_errors_total counter\n")
		fmt.Fprintf(w, "frigatecfg_config_check_errors_total %d\n", errorsN)
	})

	check()
	go func() {
		for range time.Tick(*interval) {
			check()
		}
	}()
	log.Printf("watching %s every %s; metrics on %s/metrics", *live, *interval, *listen)
	return http.ListenAndServe(*listen, nil)
}

// checkFiles runs diff over the three files and summarizes: counts by
// kind and a one-line-per-path report.  A missing live file is empty.
func checkFiles(canonical, tuning, live string) (tuningN, structuralN int, report string, err error) {
	c, t, err := readCanonicalTuning(canonical, tuning)
	if err != nil {
		return 0, 0, "", err
	}
	ln, err := readDoc(live)
	if errors.Is(err, os.ErrNotExist) {
		ln = emptyDoc()
	} else if err != nil {
		return 0, 0, "", err
	}
	drifts, err := diff(c, t, ln)
	if err != nil {
		return 0, 0, "", err
	}
	for _, d := range drifts {
		kind := "tuning"
		if d.Owned {
			tuningN++
		} else {
			structuralN++
			kind = "STRUCTURAL"
		}
		report += fmt.Sprintf("%s %s (live: %s; git: %s)\n", kind, d.Path, d.Live, d.Git)
	}
	return tuningN, structuralN, report, nil
}
