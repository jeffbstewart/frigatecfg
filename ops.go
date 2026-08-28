package main

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateTuning rejects a tuning document that carries anything
// outside the owned subtrees: structure belongs in the canonical file.
func validateTuning(tuning *yaml.Node) error {
	var bad []string
	for _, p := range leafPaths(tuning) {
		if !isOwned(p) {
			bad = append(bad, p.String())
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("tuning contains non-owned paths (move them to the canonical file):\n  %s", strings.Join(bad, "\n  "))
	}
	return nil
}

// render = canonical with every owned path present in tuning applied
// on top.  Deterministic; the first-start config.
func render(canonical, tuning *yaml.Node) (*yaml.Node, error) {
	if err := validateTuning(tuning); err != nil {
		return nil, err
	}
	out := clone(canonical)
	for _, p := range expandAll(tuning) {
		if v := get(tuning, p); v != nil {
			if err := set(out, p, v); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// merge = canonical for structure; for each owned path, the live value
// if the live file has one, else the tuning value, else whatever the
// canonical file carried.  Owned paths present in canonical or tuning
// but absent from live are NOT removed from the result: a UI cannot
// express "delete" through config/set, so absence in live is not a
// decision.  The every-start config.
func merge(canonical, tuning, live *yaml.Node) (*yaml.Node, error) {
	out, err := render(canonical, tuning)
	if err != nil {
		return nil, err
	}
	for _, p := range expandAll(live) {
		if v := get(live, p); v != nil {
			if err := set(out, p, v); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// pull = the owned paths present in live, as a tuning document.
func pull(live *yaml.Node) (*yaml.Node, error) {
	out := emptyDoc()
	for _, p := range expandAll(live) {
		if v := get(live, p); v != nil {
			if err := set(out, p, v); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Drift is one path whose value differs between what git would render
// and what is live.
type Drift struct {
	Path      string
	Owned     bool // true: tuning drift (pull it); false: structural (will be reverted)
	Live, Git string
}

// diff classifies every difference between merge(canonical, tuning,
// live) and live.  Owned paths compare tuning-vs-live; structural
// paths compare canonical-vs-live.
func diff(canonical, tuning, live *yaml.Node) ([]Drift, error) {
	rendered, err := render(canonical, tuning)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Drift
	consider := func(p path) {
		k := p.String()
		if seen[k] {
			return
		}
		seen[k] = true
		a, b := get(rendered, p), get(live, p)
		if sameValue(a, b) {
			return
		}
		out = append(out, Drift{Path: k, Owned: isOwned(p), Live: oneLine(b), Git: oneLine(a)})
	}
	// Owned subtrees compare as units so a mask list reads as one drift.
	for _, p := range expandAll(rendered, live) {
		consider(p)
	}
	// Everything else leaf by leaf, from both sides.
	for _, p := range append(leafPaths(rendered), leafPaths(live)...) {
		if isOwned(p) {
			continue
		}
		consider(p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func oneLine(n *yaml.Node) string {
	if n == nil {
		return "<absent>"
	}
	s := strings.TrimSpace(string(canonicalBytes(n)))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
