package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFiles(t *testing.T) {
	dir := t.TempDir()
	w := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c := w("garage.yaml", canonicalYAML)
	tn := w("garage.tuning.yaml", tuningYAML)

	// No live file yet: everything git renders is "missing" live.
	res, err := checkFiles(c, tn, filepath.Join(dir, "none.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if res.StructuralN == 0 {
		t.Error("absent live file should read as structural drift (git has keys live lacks)")
	}

	// Live == render: clean.
	rendered, _ := render(mustParse(t, canonicalYAML), mustParse(t, tuningYAML))
	live := w("config.yml", mustWrite(t, rendered))
	res, err = checkFiles(c, tn, live)
	if err != nil || res.TuningN != 0 || res.StructuralN != 0 || res.Report != "" {
		t.Errorf("clean case: %+v %v", res, err)
	}

	// UI changed a threshold: one tuning drift, zero structural, and
	// the pull document carries the live value.
	w("config.yml", strings.Replace(mustWrite(t, rendered), "threshold: 40", "threshold: 70", 1))
	res, err = checkFiles(c, tn, live)
	if err != nil || res.TuningN != 1 || res.StructuralN != 0 || !strings.Contains(res.Report, "tuning cameras.garage.motion.threshold") {
		t.Errorf("tuning case: %+v %v", res, err)
	}
	if !strings.Contains(res.Pull, "threshold: 70") || strings.Contains(res.Pull, "mqtt") {
		t.Errorf("pull document wrong:\n%s", res.Pull)
	}
}

func TestPullFromLogTakesLastBlock(t *testing.T) {
	logText := "2026/08/28 02:41:56 drift:\n" +
		"tuning cameras.garage.motion.threshold (live: 55; git: 40)\n" +
		"2026/08/28 02:41:56 tuning as live (the pull document):\n" +
		pullBegin + "\n" +
		"cameras:\n  garage:\n    motion:\n      threshold: 55\n" +
		pullEnd + "\n" +
		"2026/08/28 02:51:56 drift:\n" +
		"2026/08/28 02:51:56 tuning as live (the pull document):\n" +
		pullBegin + "\n" +
		"version: 0.17-0\ncameras:\n  garage:\n    motion:\n      threshold: 70\n      mask:\n        - \"0,0,1,0,1,0.1,0,0.1\"\n" +
		pullEnd + "\n"
	doc, err := pullFromLog(logText)
	if err != nil {
		t.Fatal(err)
	}
	s := mustWrite(t, doc)
	if !strings.Contains(s, "threshold: 70") || strings.Contains(s, "threshold: 55") {
		t.Errorf("did not take the last block:\n%s", s)
	}
	if !strings.Contains(s, "version: 0.17-0") {
		t.Errorf("lost a top-level owned key:\n%s", s)
	}
}

func TestPullFromLogRejectsIncompleteOrStructural(t *testing.T) {
	if _, err := pullFromLog("nothing here\n"); err == nil {
		t.Error("expected error without a block")
	}
	if _, err := pullFromLog(pullBegin + "\nmqtt:\n  enabled: true\n" + pullEnd + "\n"); err == nil {
		t.Error("expected error for a structural key in the block")
	}
}
