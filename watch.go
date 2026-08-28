package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Markers around the full pull document in the watch log.  An
// operator (or an agent that can read pod logs but cannot exec) takes
// the LAST block as the tuning file: `frigatecfg pull -from-log`.
const (
	pullBegin = "----- frigatecfg pull begin -----"
	pullEnd   = "----- frigatecfg pull end -----"
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
// Whenever the drift set changes it logs the drifting paths and, if
// any owned path drifts, the complete pull document between pullBegin
// and pullEnd -- so the log alone is enough to bring git up to date.
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
		res, err := checkFiles(*canonical, *tuning, *live)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errorsN++
			log.Printf("check failed: %v", err)
			return
		}
		tuningN, structuralN, lastOK = res.TuningN, res.StructuralN, time.Now()
		if res.Report != lastReport {
			if res.Report == "" {
				log.Print("no drift")
			} else {
				log.Print("drift:\n" + res.Report)
				if res.TuningN > 0 {
					log.Print("tuning as live (the pull document):\n" + pullBegin + "\n" + res.Pull + pullEnd)
				}
			}
			lastReport = res.Report
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

// checkResult is one drift check: counts by kind, a one-line-per-path
// report, and the full pull document (owned paths as live).
type checkResult struct {
	TuningN, StructuralN int
	Report               string
	Pull                 string
}

// checkFiles runs diff over the three files.  A missing live file is
// empty.
func checkFiles(canonical, tuning, live string) (checkResult, error) {
	var res checkResult
	c, t, err := readCanonicalTuning(canonical, tuning)
	if err != nil {
		return res, err
	}
	ln, err := readDoc(live)
	if errors.Is(err, os.ErrNotExist) {
		ln = emptyDoc()
	} else if err != nil {
		return res, err
	}
	drifts, err := diff(c, t, ln)
	if err != nil {
		return res, err
	}
	for _, d := range drifts {
		kind := "tuning"
		if d.Owned {
			res.TuningN++
		} else {
			res.StructuralN++
			kind = "STRUCTURAL"
		}
		res.Report += fmt.Sprintf("%s %s (live: %s; git: %s)\n", kind, d.Path, d.Live, d.Git)
	}
	p, err := pull(ln)
	if err != nil {
		return res, err
	}
	b, err := writeDoc(p)
	if err != nil {
		return res, err
	}
	res.Pull = string(b)
	return res, nil
}

// pullFromLog extracts the LAST pull document from a captured watch
// log (kubectl logs ... -c frigatecfg-watch).  Log-line prefixes
// (timestamps) before the markers are tolerated; the YAML lines
// between them are taken verbatim.
func pullFromLog(logText string) (*yaml.Node, error) {
	lines := strings.Split(logText, "\n")
	begin, end := -1, -1
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasSuffix(l, pullBegin):
			begin, end = i, -1
		case strings.HasSuffix(l, pullEnd):
			if begin >= 0 && end < 0 {
				end = i
			}
		}
	}
	if begin < 0 || end < 0 {
		return nil, fmt.Errorf("no complete pull block (%q ... %q) in the log", pullBegin, pullEnd)
	}
	body := strings.Join(lines[begin+1:end], "\n") + "\n"
	doc, err := parseDoc([]byte(body), "log pull block")
	if err != nil {
		return nil, err
	}
	if err := validateTuning(doc); err != nil {
		return nil, err
	}
	return doc, nil
}
