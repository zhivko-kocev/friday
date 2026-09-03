package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/render"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/spec"
)

// pullRenderMCP reverses a surface: mcp render rule: it reads the target's
// native MCP configuration, recovers each server through render.MCPInverse,
// and merges the result into the owning store's mcp.yaml — updating only
// the fields the native form actually carries for the recovered transport,
// and preserving Enabled/Agents/Tools from any existing entry, since those
// are store-only intent no native form round-trips. Servers mcp.yaml already
// has but the target's native form omits are left untouched — an absence
// there means "not enabled for this adapter" (or "scoped to a different
// one"), never "delete it everywhere".
//
// The capture runs through the forward renderer's own scoping predicate: a
// server mcp.yaml disables, or scopes to other adapters, is never rendered
// into this target, so whatever the target holds under that name is the
// user's own or another tool's. Adopting it as canonical would rewrite a
// definition friday never wrote and then ship it to every adapter the server
// IS scoped to.
//
// ok reports whether a Change was produced at all: a missing target, or a
// target with nothing to capture for a store that doesn't have mcp.yaml
// either, is silently skipped rather than manufacturing an empty file —
// matching the generic per-file pull's own "nothing to pull" convention.
func pullRenderMCP(adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string) (ch Change, ok bool, err error) {
	src := r.From[0]
	targetAbsFile := filepath.Join(targetAbs, filepath.FromSlash(r.To))
	targetInfo, err := os.Stat(targetAbsFile)
	if os.IsNotExist(err) {
		return Change{}, false, nil
	}
	if err != nil {
		return Change{}, false, fmt.Errorf("stat %s: %w", targetAbsFile, err)
	}
	targetContent, err := os.ReadFile(targetAbsFile)
	if err != nil {
		return Change{}, false, fmt.Errorf("read %s: %w", targetAbsFile, err)
	}
	base := Change{
		Adapter: adapterName, Direction: DirPull, Sources: []string{r.To},
		SrcAbs: targetAbsFile, SrcContent: targetContent, DestRel: src,
	}
	// replace runs store → target on push, so the pull has to undo it before
	// anything reads the native form back: otherwise the very marker the rule
	// exists to keep in the store is baked out of mcp.yaml on the first pull.
	recovered, unrecoverable, err := render.MCPInverse(adapterName, r.ApplyReplaceInverse(targetContent))
	if err != nil {
		unsup := base
		unsup.Action = ActionUnsupported
		unsup.Reason = fmt.Sprintf("cannot pull %s: %v", r.To, err)
		return unsup, true, nil
	}

	// mcp.yaml wants exactly one winner across sources — the same
	// single-winner rule push resolves its source through — defaulting to
	// the primary (highest precedence) for a genuinely new capture.
	root, _, resolved := rules.ResolveSingle(roots, src)
	if !resolved {
		root = primaryRoot(roots)
	}
	if reason, refused := refuseSourceWriteBack(roots, root, src); refused {
		unsup := base
		unsup.Action = ActionUnsupported
		unsup.Reason = reason
		return unsup, true, nil
	}
	storeAbsFile := filepath.Join(root.Dir, filepath.FromSlash(src))
	storeContent, err := os.ReadFile(storeAbsFile)
	creating := os.IsNotExist(err)
	if err != nil && !creating {
		return Change{}, false, fmt.Errorf("read %s: %w", storeAbsFile, err)
	}
	if creating && len(recovered) == 0 {
		return Change{}, false, nil // nothing to capture, nothing to create
	}
	existing := &spec.MCP{Version: 1}
	if !creating {
		existing, err = spec.ParseMCP(storeContent)
		if err != nil {
			unsup := base
			unsup.DestPath, unsup.OldContent = storeAbsFile, storeContent
			unsup.Action = ActionUnsupported
			unsup.Reason = fmt.Sprintf("cannot parse %s: %v", src, err)
			return unsup, true, nil
		}
	}

	merged := &spec.MCP{Version: 1, Servers: make(map[string]spec.MCPServer, len(existing.Servers)+len(recovered))}
	for name, s := range existing.Servers {
		merged.Servers[name] = s
	}
	var scopedAway []string
	captured := make(map[string]spec.MCPServer, len(recovered))
	for name, recSrv := range recovered {
		if cur, known := existing.Servers[name]; known && !(cur.IsEnabled() && cur.AppliesTo(adapterName)) {
			scopedAway = append(scopedAway, name)
			continue
		}
		srv := mergeMCPServer(existing.Servers[name], recSrv, adapterName)
		merged.Servers[name] = srv
		captured[name] = srv
	}

	existingYAML, err := yaml.Marshal(existing)
	if err != nil {
		return Change{}, false, fmt.Errorf("marshal %s: %w", src, err)
	}
	mergedYAML, err := yaml.Marshal(merged)
	if err != nil {
		return Change{}, false, fmt.Errorf("marshal %s: %w", src, err)
	}

	ch = base
	ch.DestPath = storeAbsFile
	ch.OldContent = storeContent
	ch.Mode = targetInfo.Mode().Perm()
	// Two distinct reasons a server in the target did not reach mcp.yaml, in
	// one advisory: mcp.yaml deliberately scopes it away from this adapter, or
	// the native entry has no canonical shape at all (render.MCPInverse).
	var notes []string
	if len(scopedAway) > 0 {
		sort.Strings(scopedAway)
		notes = append(notes, fmt.Sprintf("mcp.yaml scopes these away from %s: %s", adapterName, strings.Join(scopedAway, ", ")))
	}
	if len(unrecoverable) > 0 {
		notes = append(notes, "no canonical shape for: "+strings.Join(unrecoverable, ", "))
	}
	if len(notes) > 0 {
		ch.Warning = "not captured — " + strings.Join(notes, "; ")
	}
	switch {
	case creating:
		ch.Action, ch.NewContent = ActionCreate, mergedYAML
	// Compare the marshaled forms, not the decoded values: every collection
	// field is omitempty, so a nil map and an empty one are DeepEqual-distinct
	// yet byte-identical on the wire — and a phantom update here escalates to
	// a conflict prompt over a file nothing changed.
	case bytes.Equal(existingYAML, mergedYAML):
		ch.Action, ch.NewContent = ActionInSync, storeContent
	default:
		ch.Action = ActionUpdate
		ch.NewContent, err = patchMCPYAML(storeContent, captured)
		if err != nil {
			return Change{}, false, fmt.Errorf("patch %s: %w", src, err)
		}
	}
	return ch, true, nil
}

// primaryRoot is the highest-precedence root — the local store. source.Load
// builds roots lowest precedence first, so the primary is always last.
func primaryRoot(roots []rules.Root) rules.Root { return roots[len(roots)-1] }

// rootName is the value a single-winner Change reports in Root: the winning
// root's own name, or "" on a single-root store. Empty there because there is
// nothing to disambiguate, and --verbose tagging every path with "@local" on
// the overwhelmingly common single-source setup would be noise, not
// provenance.
func rootName(roots []rules.Root, winner string) string {
	if len(roots) < 2 {
		return ""
	}
	return winner
}

// refuseSourceWriteBack reports why a capture must not be written back into
// root. A configured source is a read-only clone under .sources/<name>, which
// init gitignores: `friday remote push` could never ship the edit, and the
// next `friday source update` would abort its `git pull --ff-only` on the now
// dirty tree — permanently breaking that source. A bare refusal would strand
// the user's change, so the message names both ways to keep it. Shared by
// every write-back path (pull, residue, import) so one decision has one
// wording.
func refuseSourceWriteBack(roots []rules.Root, root rules.Root, rel string) (reason string, refused bool) {
	if root.Dir == primaryRoot(roots).Dir {
		return "", false
	}
	return fmt.Sprintf(
		"%s is owned by source %q, which friday keeps read-only — the next `friday source update` would discard a local edit; either change it in that source's own repository, or copy it into the primary store, which outranks every configured source",
		rel, root.Name), true
}

// patchMCPYAML writes updates into the store's own mcp.yaml document,
// replacing only the nodes whose content actually changed. Marshaling the
// decoded *spec.MCP instead would drop every comment (spec.MCP has nowhere to
// keep them), sort the servers map alphabetically, reorder each entry's fields
// to struct order, and expand anchors into duplicated literals — a pull would
// reformat the file it is merely capturing into. Patching the parsed node tree
// leaves everything the pull did not touch byte for byte.
func patchMCPYAML(storeContent []byte, updates map[string]spec.MCPServer) ([]byte, error) {
	root, doc, err := parseYAMLDoc(storeContent)
	if err != nil {
		return nil, err
	}
	servers := config.MappingValue(doc, "servers")
	if servers == nil || servers.Kind != yaml.MappingNode {
		servers = &yaml.Node{Kind: yaml.MappingNode}
		config.SetMappingValue(doc, "servers", servers)
	}
	names := make([]string, 0, len(updates))
	for name := range updates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fresh, err := config.EncodeNode(updates[name])
		if err != nil {
			return nil, err
		}
		entry := config.MappingValue(servers, name)
		if entry == nil || entry.Kind != yaml.MappingNode {
			config.SetMappingValue(servers, name, fresh)
			continue
		}
		patchMapping(entry, fresh)
	}
	return encodeYAMLDoc(root)
}

// patchYAMLRoot writes v over the store's whole document the same way
// patchMCPYAML writes single servers: only genuinely changed nodes are
// replaced, so the user's comments and authored key order survive a pull.
func patchYAMLRoot(storeContent []byte, v any) ([]byte, error) {
	root, doc, err := parseYAMLDoc(storeContent)
	if err != nil {
		return nil, err
	}
	fresh, err := config.EncodeNode(v)
	if err != nil {
		return nil, err
	}
	patchMapping(doc, fresh)
	return encodeYAMLDoc(root)
}

// parseYAMLDoc returns content's document node and its root mapping,
// synthesizing an empty mapping for an empty (or comments-only) file so a
// first capture patches into a real tree instead of failing.
func parseYAMLDoc(content []byte) (root, mapping *yaml.Node, err error) {
	root = &yaml.Node{}
	if err := yaml.Unmarshal(content, root); err != nil {
		return nil, nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		mapping = &yaml.Node{Kind: yaml.MappingNode}
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapping}}, mapping, nil
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("document root is not a mapping")
	}
	return root, root.Content[0], nil
}

// encodeYAMLDoc re-emits a node tree at the two-space indent every schema file
// in the store is written with.
func encodeYAMLDoc(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// patchMapping overwrites dst with fresh's content in place: a key whose value
// is unchanged keeps its original node (and therefore its comment and quoting
// style), a nested mapping is patched recursively rather than replaced whole,
// and a key fresh no longer carries is dropped. dst's own key order is never
// disturbed — new keys append.
func patchMapping(dst, fresh *yaml.Node) {
	keep := make(map[string]bool, len(fresh.Content)/2)
	for i := 0; i+1 < len(fresh.Content); i += 2 {
		key, val := fresh.Content[i].Value, fresh.Content[i+1]
		keep[key] = true
		cur := config.MappingValue(dst, key)
		switch {
		case cur == nil:
			config.SetMappingValue(dst, key, val)
		case nodeEqual(cur, val):
			// unchanged — keep the user's own node, comment and all
		case cur.Kind == yaml.MappingNode && val.Kind == yaml.MappingNode:
			patchMapping(cur, val)
		default:
			config.SetMappingValue(dst, key, val)
		}
	}
	for i := 0; i+1 < len(dst.Content); {
		if keep[dst.Content[i].Value] {
			i += 2
			continue
		}
		dst.Content = append(dst.Content[:i], dst.Content[i+2:]...)
	}
}

// nodeEqual compares two nodes by content alone, ignoring the style, comments
// and position that always differ between a parsed node and a freshly encoded
// one — the whole point being to detect "this value did not change" so the
// parsed node can be kept.
func nodeEqual(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value || len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !nodeEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// mergeMCPServer updates existing (the current mcp.yaml entry for this
// server, the zero value if none yet) with recovered (freshly parsed from
// the adapter's native form). It starts from existing and overwrites only
// what this transport's native shape actually carries, so a field the shape
// cannot express — env/args on an http server, headers on a stdio one — is
// left alone rather than deleted, and the store-only fields no native form
// carries at all (Enabled, Agents, and Tools outside copilot) survive by
// simply never being touched.
//
// opencode and codex collapse http and sse onto one native shape (see
// render.MCPInverse); recovered always guesses http for that case, so an
// unchanged URL keeps existing's own transport instead of flipping a real
// sse server back to http on every pull.
func mergeMCPServer(existing, recovered spec.MCPServer, adapter string) spec.MCPServer {
	out := existing
	out.Transport = recovered.Transport
	if recovered.Transport == spec.TransportHTTP && recovered.URL == existing.URL &&
		(existing.Transport == spec.TransportHTTP || existing.Transport == spec.TransportSSE) {
		out.Transport = existing.Transport
	}
	if recovered.Transport == spec.TransportStdio {
		out.Command, out.Args, out.Env = recovered.Command, recovered.Args, recovered.Env
	} else {
		out.URL, out.Headers = recovered.URL, recovered.Headers
	}
	// copilot is the one adapter whose native form carries tools, and it
	// renders EffectiveTools — so an entry with no tools: key comes back as
	// the explicit wildcard. Adopting that would pin "every tool" as a literal
	// list in mcp.yaml for every server on the very first pull.
	if adapter == "copilot" && !(len(existing.Tools) == 0 && isWildcardTools(recovered.Tools)) {
		out.Tools = recovered.Tools
	}
	return out
}

// isWildcardTools reports the "every tool" list EffectiveTools synthesizes.
func isWildcardTools(tools []string) bool {
	return len(tools) == 1 && tools[0] == "*"
}

// --- Permissions --------------------------------------------------------

// pullRenderPermissions reverses a surface: permissions render rule
// best-effort: it reads the target's native permissions configuration,
// recovers whatever render.PermissionsInverse can map back onto friday's
// five axes into permissions.yaml, and — for claude, the one adapter whose
// native shape can safely carry it (see PermissionsInverse) — folds
// whatever it could not recognise into the native passthrough fragment
// (settings/<adapter>.json) instead of dropping it (D20).
//
// Never called for a copy-merge rule (gemini): that whole file is
// friday-owned already, so pulling it back would just read friday's own
// last render — see planPull's caller, which reports that case unsupported
// without reaching here.
func pullRenderPermissions(adapterName string, r *rules.Rule, roots []rules.Root, targetAbs string) ([]Change, error) {
	src := r.From[0]
	targetAbsFile := filepath.Join(targetAbs, filepath.FromSlash(r.To))
	targetInfo, err := os.Stat(targetAbsFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", targetAbsFile, err)
	}
	targetContent, err := os.ReadFile(targetAbsFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", targetAbsFile, err)
	}
	// Undo the rule's replace map before reading the native form back — the
	// push direction applied it on the way out. See pullRenderMCP.
	recovered, residue, unmapped, err := render.PermissionsInverse(adapterName, r.ApplyReplaceInverse(targetContent))
	if err != nil {
		return []Change{{
			Adapter: adapterName, Direction: DirPull, Sources: []string{r.To},
			SrcAbs: targetAbsFile, SrcContent: targetContent, DestRel: src,
			Action: ActionUnsupported,
			Reason: fmt.Sprintf("cannot pull %s: %v", r.To, err),
		}}, nil
	}

	root, _, resolved := rules.ResolveSingle(roots, src)
	if !resolved {
		root = primaryRoot(roots)
	}
	if reason, refused := refuseSourceWriteBack(roots, root, src); refused {
		return []Change{{
			Adapter: adapterName, Direction: DirPull, Sources: []string{r.To},
			SrcAbs: targetAbsFile, SrcContent: targetContent, DestRel: src,
			Action: ActionUnsupported, Reason: reason,
		}}, nil
	}
	storeAbsFile := filepath.Join(root.Dir, filepath.FromSlash(src))
	storeContent, err := os.ReadFile(storeAbsFile)
	creating := os.IsNotExist(err)
	if err != nil && !creating {
		return nil, fmt.Errorf("read %s: %w", storeAbsFile, err)
	}
	if creating && permissionsEmpty(recovered) && len(residue) == 0 {
		return nil, nil // nothing to capture, nothing to create
	}

	var chs []Change
	if !permissionsEmpty(recovered) || !creating {
		existing := &spec.Permissions{Version: 1}
		if !creating {
			existing, err = spec.ParsePermissions(storeContent)
			if err != nil {
				return []Change{{
					Adapter: adapterName, Direction: DirPull, Sources: []string{r.To},
					SrcAbs: targetAbsFile, SrcContent: targetContent,
					DestPath: storeAbsFile, DestRel: src, OldContent: storeContent,
					Action: ActionUnsupported,
					Reason: fmt.Sprintf("cannot parse %s: %v", src, err),
				}}, nil
			}
		}
		existingYAML, merr := yaml.Marshal(existing)
		if merr != nil {
			return nil, fmt.Errorf("marshal %s: %w", src, merr)
		}
		merged := mergePermissions(*existing, recovered)
		mergedYAML, merr := yaml.Marshal(&merged)
		if merr != nil {
			return nil, fmt.Errorf("marshal %s: %w", src, merr)
		}
		ch := Change{
			Adapter:    adapterName,
			Direction:  DirPull,
			Sources:    []string{r.To},
			SrcAbs:     targetAbsFile,
			SrcContent: targetContent,
			DestPath:   storeAbsFile,
			DestRel:    src,
			OldContent: storeContent,
			Mode:       targetInfo.Mode().Perm(),
		}
		if len(unmapped) > 0 {
			ch.Warning = "cannot recover onto an axis: " + strings.Join(unmapped, "; ")
		}
		switch {
		case creating:
			ch.Action, ch.NewContent = ActionCreate, mergedYAML
		case bytes.Equal(existingYAML, mergedYAML):
			ch.Action, ch.NewContent = ActionInSync, storeContent
		default:
			ch.Action = ActionUpdate
			ch.NewContent, merr = patchYAMLRoot(storeContent, &merged)
			if merr != nil {
				return nil, fmt.Errorf("patch %s: %w", src, merr)
			}
		}
		chs = append(chs, ch)
	}
	if len(residue) > 0 {
		rch, rerr := pullResidueChange(adapterName, r.To, residue, roots, targetAbsFile, targetContent)
		if rerr != nil {
			return nil, rerr
		}
		chs = append(chs, rch)
	}
	return chs, nil
}

// mergePermissions unions recovered's patterns into existing, axis by axis
// and tier by tier — a pattern existing already has is never duplicated,
// and nothing existing already holds is ever dropped: a pattern the native
// form no longer carries just means "not enabled for this adapter", not
// "delete it everywhere", exactly like mcp.yaml's own pull merge.
func mergePermissions(existing, recovered spec.Permissions) spec.Permissions {
	merged := existing
	merged.Version = 1
	for _, pair := range []struct{ e, r *spec.PermissionAxis }{
		{&merged.Read, &recovered.Read},
		{&merged.Write, &recovered.Write},
		{&merged.Exec, &recovered.Exec},
		{&merged.Net, &recovered.Net},
		{&merged.MCP, &recovered.MCP},
	} {
		pair.e.Allow = unionStrings(pair.e.Allow, pair.r.Allow)
		pair.e.Ask = unionStrings(pair.e.Ask, pair.r.Ask)
		pair.e.Deny = unionStrings(pair.e.Deny, pair.r.Deny)
	}
	return merged
}

// unionStrings appends add's elements not already present in existing,
// preserving existing's order and every element it already had.
func unionStrings(existing, add []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, s := range existing {
		seen[s] = true
	}
	out := existing
	for _, s := range add {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}

// permissionsEmpty reports whether p has no pattern on any axis or tier.
func permissionsEmpty(p spec.Permissions) bool {
	for _, a := range p.Axes() {
		if len(a.Axis.Allow) > 0 || len(a.Axis.Ask) > 0 || len(a.Axis.Deny) > 0 {
			return false
		}
	}
	return true
}

// pullResidueChange folds residue — raw, native-shaped JSON bytes
// PermissionsInverse could not recognise as one of friday's own axes — into
// the store's settings/<adapter>.json native passthrough fragment (Phase
// 5), the same co-owned-entry union mergeEntries already uses for merge-json
// pushes. A later push re-contributes this content through that ordinary
// passthrough rule (D20).
//
// The target keeps the entry either way — mergeEntries never removes an
// element friday did not write — so what the fragment buys is carrying the
// entry across machines through the store's own git history, which is the
// only place it would otherwise be lost.
func pullResidueChange(adapterName, ruleTo string, residue []byte, roots []rules.Root, targetAbsFile string, targetContent []byte) (Change, error) {
	fragRel := "settings/" + adapterName + ".json"
	ch := Change{
		Adapter:    adapterName,
		Direction:  DirPull,
		Sources:    []string{ruleTo},
		SrcAbs:     targetAbsFile,
		SrcContent: targetContent,
		DestRel:    fragRel,
	}
	root, _, resolved := rules.ResolveSingle(roots, fragRel)
	if !resolved {
		root = primaryRoot(roots)
	}
	if reason, refused := refuseSourceWriteBack(roots, root, fragRel); refused {
		ch.Action, ch.Reason = ActionUnsupported, reason
		return ch, nil
	}
	fragAbs := filepath.Join(root.Dir, filepath.FromSlash(fragRel))
	old, err := os.ReadFile(fragAbs)
	creating := os.IsNotExist(err)
	if err != nil && !creating {
		return Change{}, fmt.Errorf("read %s: %w", fragAbs, err)
	}
	merged, changed, err := mergeEntries(old, residue, nil)
	if err != nil {
		return Change{}, fmt.Errorf("merge residue into %s: %w", fragRel, err)
	}
	ch.DestPath = fragAbs
	ch.OldContent = old
	ch.NewContent = merged
	ch.Warning = fmt.Sprintf("residue: %s permission entries friday's axes cannot express, preserved verbatim in %s", adapterName, fragRel)
	switch {
	case creating:
		ch.Action = ActionCreate
	case changed:
		ch.Action = ActionUpdate
	default:
		ch.Action = ActionInSync
	}
	return ch, nil
}
