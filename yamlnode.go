package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// readDoc parses a YAML file into a document node.  An empty file
// yields an empty mapping document so callers never see nil.
func readDoc(name string) (*yaml.Node, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return parseDoc(b, name)
}

func parseDoc(b []byte, name string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return emptyDoc(), nil
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("%s: expected a YAML document", name)
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level must be a mapping", name)
	}
	return &doc, nil
}

func emptyDoc() *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
}

// mapping returns the mapping node behind a document or mapping node,
// or nil for anything else.
func mapping(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// get returns the node at path, or nil.
func get(doc *yaml.Node, p path) *yaml.Node {
	n := doc
	for _, k := range p {
		m := mapping(n)
		if m == nil {
			return nil
		}
		n = mapGet(m, k)
		if n == nil {
			return nil
		}
	}
	return n
}

// set places a deep copy of v at path, creating intermediate mappings.
// An existing value is replaced in place (its key keeps its position
// and comments); a new key is appended.
func set(doc *yaml.Node, p path, v *yaml.Node) error {
	if len(p) == 0 {
		return fmt.Errorf("set: empty path")
	}
	m := mapping(doc)
	if m == nil {
		return fmt.Errorf("set: root is not a mapping")
	}
	for i, k := range p {
		last := i == len(p)-1
		idx := -1
		for j := 0; j+1 < len(m.Content); j += 2 {
			if m.Content[j].Value == k {
				idx = j
				break
			}
		}
		if last {
			if idx >= 0 {
				m.Content[idx+1] = clone(v)
			} else {
				m.Content = append(m.Content, scalarKey(k), clone(v))
			}
			return nil
		}
		if idx < 0 {
			child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			m.Content = append(m.Content, scalarKey(k), child)
			m = child
			continue
		}
		next := m.Content[idx+1]
		if next.Kind != yaml.MappingNode {
			return fmt.Errorf("set: %s is not a mapping", path(p[:i+1]))
		}
		m = next
	}
	return nil
}

// del removes the key at path if present.  Empty intermediate mappings
// are left in place (harmless to Frigate, and they may carry comments).
func del(doc *yaml.Node, p path) {
	if len(p) == 0 {
		return
	}
	m := mapping(get(doc, p[:len(p)-1]))
	if m == nil {
		return
	}
	k := p[len(p)-1]
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == k {
			m.Content = append(m.Content[:j], m.Content[j+2:]...)
			return
		}
	}
}

func scalarKey(k string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
}

func clone(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = clone(ch)
	}
	return &c
}

// leafPaths lists every leaf (scalar, sequence, or empty mapping)
// under the document as a concrete path, so callers can classify a
// whole file path by path.
func leafPaths(doc *yaml.Node) []path {
	var out []path
	var walk func(n *yaml.Node, sofar path)
	walk = func(n *yaml.Node, sofar path) {
		m := mapping(n)
		if m == nil || len(m.Content) == 0 {
			out = append(out, append(path(nil), sofar...))
			return
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			walk(m.Content[i+1], append(sofar, m.Content[i].Value))
		}
	}
	if m := mapping(doc); m != nil {
		for i := 0; i+1 < len(m.Content); i += 2 {
			walk(m.Content[i+1], path{m.Content[i].Value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// canonicalBytes renders a node with comments and styling stripped,
// for value comparison only.
func canonicalBytes(n *yaml.Node) []byte {
	if n == nil {
		return nil
	}
	c := clone(n)
	stripComments(c)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(c)
	_ = enc.Close()
	return buf.Bytes()
}

func stripComments(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	n.Style = 0
	for _, c := range n.Content {
		stripComments(c)
	}
}

// sameValue compares two nodes by canonical form.  A one-element
// sequence of a scalar equals that scalar: Frigate accepts a single
// motion mask (and similar list-or-string fields) either way and
// rewrites the file in scalar form, which must not read as drift.
func sameValue(a, b *yaml.Node) bool {
	return bytes.Equal(canonicalBytes(unwrapSingleton(a)), canonicalBytes(unwrapSingleton(b)))
}

func unwrapSingleton(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.SequenceNode && len(n.Content) == 1 && n.Content[0].Kind == yaml.ScalarNode {
		return n.Content[0]
	}
	return n
}

// writeDoc encodes a document with two-space indent.
func writeDoc(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
