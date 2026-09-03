package lint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
	"github.com/zhivko-kocev/friday/internal/source"
)

// AgnixStatus reports how a RunAgnix call went, distinguishing "no linter
// installed" (an informational hint) from "found it but couldn't use it" (a
// warning worth the raw exit status) from a clean run.
type AgnixStatus int

const (
	// AgnixNotFound means no agnix binary is on PATH. Not an error — most
	// installs won't have it, and friday's own render-graph checks still ran.
	AgnixNotFound AgnixStatus = iota
	// AgnixFailed means a binary was found but its output could not be used
	// (non-SARIF output, a process that could not start, or the like).
	AgnixFailed
	// AgnixOK means agnix ran and its SARIF output parsed; Findings holds
	// whatever it reported (which may be empty — a clean report).
	AgnixOK
)

// agnixBinary is the PATH lookup name, overridable by tests so they never
// depend on the real agent-sh/agnix binary being installed.
var agnixBinary = "agnix"

// agnixLookPath resolves agnixBinary. A package-level var for the same
// reason internal/session's opencodeLookPath is: tests inject a fake so
// AgnixNotFound and AgnixOK are both reachable without depending on whether
// a real agnix binary happens to be on the machine running the tests.
var agnixLookPath = exec.LookPath

// agnixRunner runs `agnix --format sarif <dir>` and returns exactly what
// exec.Cmd.Output would: stdout, and any error — including *exec.ExitError
// for a non-zero exit, which agnix uses to mean "found issues", not
// "crashed". Extracted as a seam so tests can exercise the SARIF-parsing
// and classification logic in runAgnixRoot against synthetic payloads
// without a real agnix binary on PATH.
type agnixRunner func(agnixPath, dir string) ([]byte, error)

// agnixTimeout bounds the agnix child process and agnixMaxOutput bounds what
// friday buffers from it. A wedged or runaway linter must never hang friday
// doctor or grow its memory without limit — the same contract
// internal/session's opencodeTimeout enforces, with a larger budget because
// agnix walks a whole store instead of answering one metadata query. Both are
// vars only so the tests that drive a real child process can shrink them
// instead of waiting out the production budget.
var (
	agnixTimeout   = 30 * time.Second
	agnixMaxOutput = 8 << 20
)

// agnixRun is the real agnixRunner used outside tests.
var agnixRun agnixRunner = func(agnixPath, dir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agnixTimeout)
	defer cancel()
	// "--" so a store dir that happens to start with a dash is never read as
	// a flag by whatever agnix's argument parser does with it.
	cmd := exec.CommandContext(ctx, agnixPath, "--format", "sarif", "--", dir)
	out := &cappedBuffer{limit: agnixMaxOutput}
	cmd.Stdout = out
	// Killing the child on deadline does not close a pipe a grandchild still
	// holds, and Wait blocks until the stdout copy finishes — WaitDelay is
	// what actually guarantees this call returns.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("agnix timed out after %s", agnixTimeout)
	case out.truncated:
		return nil, fmt.Errorf("agnix produced more than %d bytes of output", agnixMaxOutput)
	}
	return out.buf.Bytes(), err
}

// cappedBuffer keeps the first limit bytes written to it and discards the
// rest, recording that it did. Discarding beats failing the write: an error
// here would close the pipe under the child and race its exit status, while
// the deadline already bounds how long a runaway process gets to produce
// bytes nobody will read.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room < len(p) {
		c.truncated = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

// RunAgnix delegates content linting to the external agnix binary
// (github.com/agent-sh/agnix) when one is on PATH, running it once per
// source root with `--format sarif` and translating its results into
// friday's Finding vocabulary (D26): Rule prefixed "agnix/" and Origin set
// to "agnix", so a caller can separate delegated findings from friday's own
// strictly render-graph checks (dest-collision, drift, hooks staleness,
// source-shadow, source-manifest, mcp-secret, store structure) without
// parsing Rule. A finding's Path gets the same "<source>:" prefix lintRoot
// uses for a non-primary root. Findings pass through the same
// .friday-doctor.yaml disable list as friday's own checks, so
// `disable: ["agnix/CC-SK-007"]` silences a delegated rule exactly like a
// native one.
//
// agnix's own exit code is not itself a failure signal: like most linters it
// exits non-zero simply because it found issues, so any exit code is
// accepted as long as stdout is SARIF 2.x — the declared version is checked,
// because valid JSON in some other shape would otherwise read as a clean
// report. Empty, unparseable, wrong-version, over-long, and timed-out
// responses all degrade to AgnixFailed carrying the raw detail: this
// integration must never fail friday doctor itself, and must never claim
// health it did not verify.
func RunAgnix(cfg *config.Config) (findings []Finding, status AgnixStatus, detail string) {
	agnixPath, err := agnixLookPath(agnixBinary)
	if err != nil {
		return nil, AgnixNotFound, ""
	}
	set, err := source.Load(cfg)
	if err != nil {
		return nil, AgnixFailed, err.Error()
	}
	sourcesDirName := filepath.Base(cfg.SourcesDir())
	for _, root := range set.Roots() {
		if _, statErr := os.Stat(root.Dir); statErr != nil {
			continue // source declared but not yet cloned — same skip lintRoot applies
		}
		rootFindings, rerr := runAgnixRoot(agnixPath, root, sourcesDirName)
		if rerr != nil {
			return nil, AgnixFailed, rerr.Error()
		}
		findings = append(findings, rootFindings...)
	}
	return applyIgnores(cfg.StoreDir, findings), AgnixOK, ""
}

// runAgnixRoot runs agnix over a single source root and maps its SARIF
// results onto Finding.
func runAgnixRoot(agnixPath string, root rules.Root, sourcesDirName string) ([]Finding, error) {
	out, err := agnixRun(agnixPath, root.Dir)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("run agnix: %w", err)
		}
		// agnix exits non-zero when it finds issues, not just when it
		// crashes — that alone is not a failure. Only give up here if it
		// also produced nothing to parse.
		if len(out) == 0 {
			return nil, fmt.Errorf("agnix exited %d with no output", exitErr.ExitCode())
		}
	}
	var doc sarifDocument
	if jerr := json.Unmarshal(out, &doc); jerr != nil {
		return nil, fmt.Errorf("parse agnix SARIF output: %w", jerr)
	}
	// Go ignores unknown fields, so a future agnix emitting valid JSON in a
	// different shape would unmarshal into an empty document and read as a
	// clean bill of health. Version skew must degrade visibly (AgnixFailed),
	// never silently — so the schema version is the contract, not a hint.
	if !strings.HasPrefix(doc.Version, "2.") {
		return nil, fmt.Errorf("agnix reported SARIF version %q, want 2.x — friday cannot read this output", doc.Version)
	}
	var findings []Finding
	for _, run := range doc.Runs {
		for _, res := range run.Results {
			path := "(unknown)"
			if len(res.Locations) > 0 {
				path = res.Locations[0].PhysicalLocation.ArtifactLocation.URI
			}
			if root.Name == "local" {
				// The source clones live inside the primary root
				// (<store>/.sources/<name>), and each is scanned again on its
				// own iteration — correctly prefixed. Dropping them here is
				// the agnix-side equivalent of lintRoot's dot-dir SkipDir:
				// without it every source file is reported twice, once
				// mislabelled as the user's own.
				if underSources(path, sourcesDirName) {
					continue
				}
			} else {
				path = root.Name + ":" + path
			}
			sev := Warn
			if res.Level == "error" {
				sev = Error
			}
			findings = append(findings, Finding{
				Path:     path,
				Rule:     "agnix/" + res.RuleID,
				Severity: sev,
				Msg:      res.Message.Text,
				Origin:   "agnix",
			})
		}
	}
	return findings, nil
}

// underSources reports whether a SARIF artifact URI points into the store's
// .sources tree. The directory name comes from config.SourcesDir() rather
// than a second literal here. agnix may report a path store-relative,
// "./"-prefixed, or absolute (and as a file:// URI), so all four are
// normalized before the prefix test.
func underSources(uri, sourcesDirName string) bool {
	u := strings.TrimPrefix(strings.ReplaceAll(uri, "\\", "/"), "file://")
	u = strings.TrimPrefix(u, "./")
	return strings.HasPrefix(u, sourcesDirName+"/") || strings.Contains(u, "/"+sourcesDirName+"/")
}

// sarifDocument is the minimal subset of the SARIF 2.1.0 schema
// (https://docs.oasis-open.org/sarif/sarif/v2.1.0) needed to recover
// agnix's findings: which rule fired, at what severity, with what message,
// against which file. Everything else in a SARIF log (the tool's rule
// catalog, fingerprints, code-flow graphs) is unused here.
type sarifDocument struct {
	// Version is load-bearing, not decoration: it is the only field that
	// distinguishes SARIF friday can read from valid JSON in some other
	// shape, which would otherwise unmarshal into zero findings.
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"` // "error", "warning", "note", or "none"
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}
