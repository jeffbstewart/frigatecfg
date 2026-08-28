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
	_, sn, _, err := checkFiles(c, tn, filepath.Join(dir, "none.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if sn == 0 {
		t.Error("absent live file should read as structural drift (git has keys live lacks)")
	}

	// Live == render: clean.
	rendered, _ := render(mustParse(t, canonicalYAML), mustParse(t, tuningYAML))
	live := w("config.yml", mustWrite(t, rendered))
	tcount, scount, report, err := checkFiles(c, tn, live)
	if err != nil || tcount != 0 || scount != 0 || report != "" {
		t.Errorf("clean case: %d %d %q %v", tcount, scount, report, err)
	}

	// UI changed a threshold: one tuning drift, zero structural.
	w("config.yml", strings.Replace(mustWrite(t, rendered), "threshold: 40", "threshold: 70", 1))
	tcount, scount, report, err = checkFiles(c, tn, live)
	if err != nil || tcount != 1 || scount != 0 || !strings.Contains(report, "tuning cameras.garage.motion.threshold") {
		t.Errorf("tuning case: %d %d %q %v", tcount, scount, report, err)
	}
}
