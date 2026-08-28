// frigatecfg keeps a Frigate config.yml under two owners: git for
// structure (the canonical file) and the Frigate web UI for tuning
// (masks, zones, motion thresholds, ... -- the paths the UI writes
// through /api/config/set).  See README.md for the model.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage: frigatecfg <command> [flags]

  render  -canonical F -tuning F [-out F]           first-start config
  merge   -canonical F -tuning F -live F [-out F]   every-start config
  pull    -live F | -from-log F [-out F]            owned paths -> tuning
  diff    -canonical F -tuning F -live F            classify drift
  watch   -canonical F -tuning F -live F [-interval D] [-listen A]
                                                    re-diff on an interval, serve /metrics
  paths                                             print the owned paths

Exit codes for diff: 0 no drift, 2 tuning drift only, 3 structural drift.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "render":
		err = cmdRender(os.Args[2:])
	case "merge":
		err = cmdMerge(os.Args[2:])
	case "pull":
		err = cmdPull(os.Args[2:])
	case "diff":
		err = cmdDiff(os.Args[2:])
	case "watch":
		err = cmdWatch(os.Args[2:])
	case "paths":
		for _, p := range ownedPatterns {
			fmt.Println(p)
		}
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			os.Exit(int(ec))
		}
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "frigatecfg:", err)
		}
		os.Exit(1)
	}
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	return fs
}

func required(fs *flag.FlagSet, vals map[string]*string) error {
	for name, v := range vals {
		if *v == "" {
			fs.Usage()
			return fmt.Errorf("-%s is required", name)
		}
	}
	return nil
}

func emit(out string, doc *yaml.Node) error {
	b, err := writeDoc(doc)
	if err != nil {
		return err
	}
	if out == "" || out == "-" {
		_, err = os.Stdout.Write(b)
		return err
	}
	return writeFileAtomic(out, b)
}

// writeFileAtomic writes via a sibling temp file and rename so a
// reader (Frigate starting up) never sees a partial config.
func writeFileAtomic(name string, b []byte) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func cmdRender(args []string) error {
	fs := newFlags("render")
	canonical := fs.String("canonical", "", "canonical (git-owned) config file")
	tuning := fs.String("tuning", "", "tuning (UI-owned paths) file; may be empty or absent")
	out := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := required(fs, map[string]*string{"canonical": canonical, "tuning": tuning}); err != nil {
		return err
	}
	c, t, err := readCanonicalTuning(*canonical, *tuning)
	if err != nil {
		return err
	}
	doc, err := render(c, t)
	if err != nil {
		return err
	}
	return emit(*out, doc)
}

func cmdMerge(args []string) error {
	fs := newFlags("merge")
	canonical := fs.String("canonical", "", "canonical (git-owned) config file")
	tuning := fs.String("tuning", "", "tuning (UI-owned paths) file; may be empty or absent")
	live := fs.String("live", "", "the live config.yml Frigate has been writing")
	out := fs.String("out", "", "output file (default stdout); may equal -live")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := required(fs, map[string]*string{"canonical": canonical, "tuning": tuning, "live": live}); err != nil {
		return err
	}
	c, t, err := readCanonicalTuning(*canonical, *tuning)
	if err != nil {
		return err
	}
	l, err := readDoc(*live)
	if errors.Is(err, os.ErrNotExist) {
		l = emptyDoc() // first start: nothing live yet, merge == render
	} else if err != nil {
		return err
	}
	doc, err := merge(c, t, l)
	if err != nil {
		return err
	}
	return emit(*out, doc)
}

func cmdPull(args []string) error {
	fs := newFlags("pull")
	live := fs.String("live", "", "the live config.yml (or a GET /api/config/raw capture)")
	fromLog := fs.String("from-log", "", "a captured watch log; take its LAST pull block instead of -live")
	out := fs.String("out", "", "tuning file to write (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*live == "") == (*fromLog == "") {
		fs.Usage()
		return fmt.Errorf("exactly one of -live or -from-log is required")
	}
	if *fromLog != "" {
		b, err := os.ReadFile(*fromLog)
		if err != nil {
			return err
		}
		doc, err := pullFromLog(string(b))
		if err != nil {
			return err
		}
		return emit(*out, doc)
	}
	l, err := readDoc(*live)
	if err != nil {
		return err
	}
	doc, err := pull(l)
	if err != nil {
		return err
	}
	return emit(*out, doc)
}

func cmdDiff(args []string) error {
	fs := newFlags("diff")
	canonical := fs.String("canonical", "", "canonical (git-owned) config file")
	tuning := fs.String("tuning", "", "tuning (UI-owned paths) file; may be empty or absent")
	live := fs.String("live", "", "the live config.yml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := required(fs, map[string]*string{"canonical": canonical, "tuning": tuning, "live": live}); err != nil {
		return err
	}
	c, t, err := readCanonicalTuning(*canonical, *tuning)
	if err != nil {
		return err
	}
	l, err := readDoc(*live)
	if err != nil {
		return err
	}
	drifts, err := diff(c, t, l)
	if err != nil {
		return err
	}
	structural := false
	for _, d := range drifts {
		kind := "tuning   "
		if !d.Owned {
			kind = "STRUCTURAL"
			structural = true
		}
		fmt.Printf("%s %s\n    live: %s\n    git:  %s\n", kind, d.Path, d.Live, d.Git)
	}
	switch {
	case structural:
		return exitCode(3)
	case len(drifts) > 0:
		return exitCode(2)
	}
	return nil
}

// readCanonicalTuning reads both inputs; a missing tuning file is an
// empty tuning document (a camera with no UI tuning yet is normal).
func readCanonicalTuning(canonical, tuning string) (*yaml.Node, *yaml.Node, error) {
	c, err := readDoc(canonical)
	if err != nil {
		return nil, nil, err
	}
	t, err := readDoc(tuning)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, emptyDoc(), nil
		}
		return nil, nil, err
	}
	return c, t, nil
}
