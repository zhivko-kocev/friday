package config

import (
	"bytes"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/zhivko-kocev/friday/internal/atomicio"
)

// EditManifest applies a targeted edit to friday.yaml and writes it back.
//
// Save() marshals the whole Config struct, which cannot emit comments and
// re-emits every preset rule the user never touched — turning one `source
// add` into a hundred-line reformat of a git-tracked file. EditManifest
// instead mutates the parsed yaml.Node tree, so every key, comment, and
// ordering decision outside the edited node survives byte for byte.
//
// fn receives the top-level mapping node. Returning an error aborts the
// write.
//
// The result is re-validated before it lands: every writer has to leave a
// manifest friday can still open, because the command that would undo the
// damage loads it first. Without that check the guarantee held only by the
// current caller's good manners.
func EditManifest(path string, fn func(doc *yaml.Node) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	doc, err := mappingRoot(&root)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := fn(doc); err != nil {
		return err
	}
	out, err := marshalNode(&root)
	if err != nil {
		return err
	}
	if err := validateManifestBytes(out); err != nil {
		return fmt.Errorf("edit would make %s unloadable: %w", path, err)
	}
	return atomicio.WriteFile(path, out, 0o644)
}

// validateManifestBytes runs the structural half of load() over a candidate
// manifest: the version, every adapter invariant, and the source rules. It
// deliberately does not resolve paths or touch disk — a writer is checking
// that the document parses and validates, not that this machine can render it.
func validateManifestBytes(data []byte) error {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return err
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported version %d", cfg.Version)
	}
	for _, name := range cfg.AdapterNames() {
		a := cfg.Adapters[name]
		if a == nil {
			return fmt.Errorf("adapter %q is empty", name)
		}
		if a.Target == "" {
			return fmt.Errorf("adapter %q: target is required", name)
		}
	}
	return validateSources(cfg.Sources)
}

// mappingRoot unwraps the document node and reports a manifest whose root is
// not a mapping, which every other accessor would otherwise index blindly.
func mappingRoot(root *yaml.Node) (*yaml.Node, error) {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, fmt.Errorf("manifest is empty")
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("manifest root is not a mapping")
	}
	return n, nil
}

// marshalNode re-encodes a node tree with the indentation yaml.Marshal uses,
// so an edited manifest stays diff-comparable with a generated one.
func marshalNode(root *yaml.Node) ([]byte, error) {
	var buf bytesBuffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.b, nil
}

// bytesBuffer is a minimal io.Writer — avoids pulling bytes into this file's
// import set for one accumulation.
type bytesBuffer struct{ b []byte }

func (w *bytesBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

// MappingValue returns the value node for key in a mapping node, or nil when
// the key is absent. Mapping content alternates key, value.
func MappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// SetMappingValue sets key to val in a mapping node, appending the pair when
// key is absent so an edit never reorders existing entries.
func SetMappingValue(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

// DeleteMappingKey removes key and its value from a mapping node, reporting
// whether anything was removed.
func DeleteMappingKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// RenameMappingKey renames a key in place, preserving its position, its value
// node, and any comment attached to either. Reports whether the key existed.
func RenameMappingKey(m *yaml.Node, from, to string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == from {
			m.Content[i].Value = to
			return true
		}
	}
	return false
}

// EncodeNode marshals v into a yaml.Node suitable for splicing into a tree.
func EncodeNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}
