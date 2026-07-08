// Package lint statically checks a friday store: structural problems
// (malformed frontmatter, destination collisions) and best-practice advisories
// (oversized or overlong instruction files, broken references, weak skill
// descriptions). A quality gate for `friday doctor` — a one-shot linter for how
// you author your agent config, never a resident process. Every rule is a
// static check over files on disk; runtime operating judgment (context budget,
// session discipline) is deliberately out of scope.
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

const (
	// maxFileSize is the hard warn threshold — any file past this bloats a
	// context window regardless of type.
	maxFileSize = 64 * 1024
	// maxInstructionLines is the best-practice soft limit for an entry or rule
	// file: past this, an instruction file is usually trying to do too much and
	// should be split.
	maxInstructionLines = 200
	// minSkillDescription is the shortest a skill description can be and still
	// work as a trigger the agent can match on.
	minSkillDescription = 20
)

// Severity ranks a finding. Only Error fails `friday doctor`; Warn is
// advisory — the "best-practice advisor" tone.
type Severity int

const (
	Warn  Severity = iota // advisory: a best practice you're diverging from
	Error                 // a structural problem that will misbehave
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warn"
}

// Finding is one lint hit. Rule is a stable, grep-able slug that doubles as
// the id you disable in .friday-doctor.yaml.
type Finding struct {
	Path     string // store-relative (or "adapter:dest" for collisions)
	Rule     string
	Severity Severity
	Msg      string
}

// mdLink captures the path of a relative markdown link — no scheme, no
// pure-anchor links.
var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)#:\s]+)\)`)

// Run lints the store and the adapter rule set, then drops any findings whose
// rule is disabled in <store>/.friday-doctor.yaml. The error return is for
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
			findings = append(findings, Finding{Path: rel, Rule: "oversized", Severity: Warn,
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
			findings = append(findings, Finding{Path: rel, Rule: "frontmatter", Severity: Error, Msg: ferr.Error()})
		}
		findings = append(findings, checkInstructionLength(rel, data)...)
		findings = append(findings, checkSkillDescription(rel, data)...)
		findings = append(findings, checkRefs(cfg.StoreDir, rel, data)...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	findings = append(findings, checkDestCollisions(cfg)...)
	return applyIgnores(cfg.StoreDir, findings), nil
}

// isInstructionFile reports whether rel is an entry file or a behaviour rule —
// the files kept short for a healthy context budget.
func isInstructionFile(rel string) bool {
	switch rel {
	case "core.md", "core/core.md", "identity.md":
		return true
	}
	return strings.HasPrefix(rel, "rules/")
}

// checkInstructionLength warns when an entry or rule file runs long — the
// single most-cited CLAUDE.md best practice is to keep these focused.
func checkInstructionLength(rel string, content []byte) []Finding {
	if !isInstructionFile(rel) {
		return nil
	}
	lines := strings.Count(string(content), "\n") + 1
	if lines <= maxInstructionLines {
		return nil
	}
	return []Finding{{Path: rel, Rule: "long-instructions", Severity: Warn,
		Msg: fmt.Sprintf("%d lines (> %d) — split focused topics into separate rules/*.md so agents load only what they need", lines, maxInstructionLines)}}
}

// checkSkillDescription warns when a skill lacks a usable description. A
// skill's description is the trigger the agent matches on; a missing or terse
// one means the skill silently never fires.
func checkSkillDescription(rel string, content []byte) []Finding {
	if !(strings.HasPrefix(rel, "skills/") && strings.HasSuffix(rel, "/SKILL.md")) {
		return nil
	}
	fields, _ := frontmatter.Parse(string(content))
	desc, _ := fields["description"].(string)
	desc = strings.TrimSpace(desc)
	switch {
	case desc == "":
		return []Finding{{Path: rel, Rule: "skill-description", Severity: Warn,
			Msg: "no description in frontmatter — the description is the trigger the agent matches on; without it the skill never fires"}}
	case len(desc) < minSkillDescription:
		return []Finding{{Path: rel, Rule: "skill-description", Severity: Warn,
			Msg: fmt.Sprintf("description is only %d chars — make it a specific sentence describing when to use the skill", len(desc))}}
	}
	return nil
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
		out = append(out, Finding{Path: rel, Rule: "broken-ref", Severity: Warn, Msg: fmt.Sprintf("link target %q not found", ref)})
	}
	return out
}

// checkDestCollisions plans a dry-run push and flags destinations written by
// more than one change — later rules silently win, which is never intended.
func checkDestCollisions(cfg *config.Config) []Finding {
	changes, err := engine.Push(cfg, engine.Options{DryRun: true})
	if err != nil {
		return []Finding{{Path: "", Rule: "plan", Severity: Error, Msg: err.Error()}}
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
			out = append(out, Finding{Path: ch.Adapter + ":" + ch.DestRel, Rule: "dest-collision", Severity: Error,
				Msg: fmt.Sprintf("written by multiple rules (%s) — the last one wins", strings.Join(seen[key], " and "))})
		}
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
