package main

import (
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Owned paths: the dotted key paths the Frigate web UI writes through
// PUT /api/config/set (enumerated from the v0.17.0 web/src call
// sites), plus "version", which Frigate's startup migration rewrites.
// A pattern owns the whole subtree at that path.  "*" matches any one
// map key.  Everything NOT under one of these is structural and
// belongs to the canonical file in git.
var ownedPatterns = []string{
	"version",
	"cameras.*.motion.mask",
	"cameras.*.motion.threshold",
	"cameras.*.motion.contour_area",
	"cameras.*.motion.improve_contrast",
	"cameras.*.zones",
	"cameras.*.objects.mask",
	"cameras.*.objects.filters.*.mask",
	"cameras.*.review",
	"cameras.*.detect.annotation_offset",
	"camera_groups",
	"notifications.enabled",
	"semantic_search.enabled",
	"semantic_search.model_size",
	"face_recognition.enabled",
	"face_recognition.model_size",
	"lpr.enabled",
	"classification.bird.enabled",
}

// A path is a sequence of map keys from the document root.
type path []string

func (p path) String() string { return strings.Join(p, ".") }

func parsePattern(s string) path { return strings.Split(s, ".") }

// matchesPrefix reports whether the pattern (with wildcards) is a
// prefix of the concrete path, i.e. the path lies inside the owned
// subtree.
func matchesPrefix(pattern, concrete path) bool {
	if len(pattern) > len(concrete) {
		return false
	}
	for i, pe := range pattern {
		if pe != "*" && pe != concrete[i] {
			return false
		}
	}
	return true
}

// isOwned reports whether a concrete path lies inside any owned subtree.
func isOwned(concrete path) bool {
	for _, ps := range ownedPatterns {
		if matchesPrefix(parsePattern(ps), concrete) {
			return true
		}
	}
	return false
}

// expand enumerates the concrete paths that exist in doc and match the
// pattern exactly (same length), resolving wildcards against the
// document's map keys.
func expand(doc *yaml.Node, pattern path) []path {
	var out []path
	var walk func(n *yaml.Node, i int, sofar path)
	walk = func(n *yaml.Node, i int, sofar path) {
		if i == len(pattern) {
			out = append(out, append(path(nil), sofar...))
			return
		}
		m := mapping(n)
		if m == nil {
			return
		}
		if pattern[i] == "*" {
			for k := 0; k+1 < len(m.Content); k += 2 {
				key := m.Content[k].Value
				walk(m.Content[k+1], i+1, append(sofar, key))
			}
			return
		}
		if v := mapGet(m, pattern[i]); v != nil {
			walk(v, i+1, append(sofar, pattern[i]))
		}
	}
	walk(doc, 0, nil)
	return out
}

// expandAll enumerates every concrete owned path present in any of the
// given documents, deduplicated and sorted.
func expandAll(docs ...*yaml.Node) []path {
	seen := map[string]path{}
	for _, ps := range ownedPatterns {
		pat := parsePattern(ps)
		for _, d := range docs {
			for _, p := range expand(d, pat) {
				seen[p.String()] = p
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]path, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}
