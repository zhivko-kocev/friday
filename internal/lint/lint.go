// Package lint statically checks a friday store: malformed frontmatter,
// oversized files, broken relative references, and rules that write the
// same destination twice. A quality gate to run before `friday remote push`.
package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/engine"
	"github.com/zhivko-kocev/friday/internal/frontmatter"
)

// maxFileSize is the warn threshold — agent instruction files past this are
// almost certainly bloating someone's context window.
const maxFileSize = 64 * 1024

// Finding is one lint hit. Rule is a short slug (frontmatter, oversized,
// broken-ref, dest-collision) so output stays grep-able.
type Finding struct {
	Path string // store-relative (or dest-relative for collisions)
	Rule string
	Msg  string
}

// mdLink captures the path of a relative markdown link — no scheme, no
// pure-anchor links.
var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)#:\s]+)\)`)

// Run lints the store and the adapter rule set. The error return is for
// I/O-level failures only; lint problems come back as findings.
func Run(cfg *config.Config) ([]Finding, error) {
	var findings []Finding

	err := filepath.WalkDir(cfg.StoreDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != cfg.StoreDir {
				return filepath.SkipDir // .git and friends
			}
			return nil
		}
		rel, rerr := filepath.Rel(cfg.StoreDir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		info, ierr := d.Info()
		if ierr == nil && info.Size() > maxFileSize {
			findings = append(findings, Finding{Path: rel, Rule: "oversized",
				Msg: fmt.Sprintf("%d KiB — agent instruction files this large bloat context windows", info.Size()/1024)})
		}
		if !strings.HasSuffix(rel, ".md") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if _, _, ferr := frontmatter.ParseStrict(string(data)); ferr != nil {
			findings = append(findings, Finding{Path: rel, Rule: "frontmatter", Msg: ferr.Error()})
		}
		findings = append(findings, checkRefs(cfg.StoreDir, rel, data)...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	findings = append(findings, checkDestCollisions(cfg)...)
	return findings, nil
}

// checkRefs flags relative markdown links that resolve to nothing, checked
// against both the linking file's directory and the store root.
func checkRefs(storeDir, rel string, content []byte) []Finding {
	var out []Finding
	for _, m := range mdLink.FindAllSubmatch(content, -1) {
		ref := string(m[1])
		if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") || strings.Contains(ref, "${") {
			continue // absolute / home / templated — not checkable
		}
		fromFile := filepath.Join(storeDir, filepath.Dir(filepath.FromSlash(rel)), filepath.FromSlash(ref))
		fromRoot := filepath.Join(storeDir, filepath.FromSlash(ref))
		if fileExists(fromFile) || fileExists(fromRoot) {
			continue
		}
		out = append(out, Finding{Path: rel, Rule: "broken-ref", Msg: fmt.Sprintf("link target %q not found", ref)})
	}
	return out
}

// checkDestCollisions plans a dry-run push and flags destinations written by
// more than one change — later rules silently win, which is never intended.
func checkDestCollisions(cfg *config.Config) []Finding {
	changes, err := engine.Push(cfg, engine.Options{DryRun: true})
	if err != nil {
		return []Finding{{Path: "", Rule: "plan", Msg: err.Error()}}
	}
	seen := map[string][]string{} // destPath → sources
	var out []Finding
	for _, ch := range changes {
		switch ch.Action {
		case engine.ActionMissingSource, engine.ActionUnsupported:
			continue
		}
		key := ch.Adapter + ":" + ch.DestPath
		seen[key] = append(seen[key], strings.Join(ch.Sources, "+"))
		if len(seen[key]) == 2 {
			out = append(out, Finding{Path: ch.Adapter + ":" + ch.DestRel, Rule: "dest-collision",
				Msg: fmt.Sprintf("written by multiple rules (%s) — the last one wins", strings.Join(seen[key], " and "))})
		}
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
