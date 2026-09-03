package secret

import (
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/spec"
)

func hasKey(findings []Finding, key string) bool {
	for _, f := range findings {
		if f.Key == key {
			return true
		}
	}
	return false
}

// jwt is a syntactically complete header.payload.signature token; the two
// `.` separators are exactly what defeated the old single-blob matcher.
const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"

// Every vendor-shaped fixture below is assembled at run time rather than
// written as a literal. The detector keys on a vendor prefix, a charset and
// a minimum length (see vendorPatterns) and applies no checksum or entropy
// test, so a repeated-character body takes exactly the same match path as a
// live key would — while leaving no string in this file that a repository
// secret scanner can mistake for a real credential and reject a push over.
// Merely writing an obviously-fake literal is not enough: GitHub push
// protection flags a sequential dop_v1_0123... body just the same, so the
// absent literal is the only thing that reliably works.
func vendorFixture(prefix string, bodyLen int) string {
	return prefix + strings.Repeat("a", bodyLen)
}

var (
	openAIKey       = vendorFixture("sk-", 40)
	ghClassicPAT    = vendorFixture("ghp_", 36)
	ghFineGrained   = vendorFixture("github_pat_", 60)
	ghOAuthToken    = vendorFixture("gho_", 36)
	ghUserToken     = vendorFixture("ghu_", 36)
	ghServerToken   = vendorFixture("ghs_", 36)
	slackBotToken   = vendorFixture("xoxb-", 30)
	googleAPIKey    = vendorFixture("AIza", 35)
	googleOAuth     = vendorFixture("ya29.", 45)
	gitlabPAT       = vendorFixture("glpat-", 20)
	digitalOceanPAT = vendorFixture("dop_v1_", 64)
)

// credentialCorpus is the audit's executed corpus: realistic credential
// strings run against the pre-v1.0 detector, of which only five matched.
// Every documented false negative is pinned true here and every documented
// false positive is pinned false, so a future edit to the vendor table, the
// key list, or the blob rule cannot silently reopen one. None of these are
// real credentials — they are vendor-documented examples and dummy values.
var credentialCorpus = []struct {
	name  string
	key   string
	value string
	want  bool
}{
	// Shapes the pre-v1.0 detector already caught.
	{"openai key", "OPENAI_API_KEY", openAIKey, true},
	{"github classic pat", "GITHUB_TOKEN", ghClassicPAT, true},
	{"github fine-grained pat", "GITHUB_TOKEN", ghFineGrained, true},
	{"slack bot token", "SLACK_BOT_TOKEN", slackBotToken, true},
	{"google api key", "GOOGLE_API_KEY", googleAPIKey, true},
	{"google oauth token", "GOOGLE_TOKEN", googleOAuth, true},
	{"pem private key", "SERVICE_ACCOUNT", "-----BEGIN PRIVATE KEY-----\nMIIEvQ...\n-----END PRIVATE KEY-----", true},

	// Documented false negatives: a wrapper defeated the start anchor.
	{"bearer-wrapped token", "Authorization", "Bearer " + ghClassicPAT, true},
	{"flag-wrapped token", "NOTES", "--token=" + ghClassicPAT, true},

	// Documented false negatives: the vendor was missing from the list.
	{"github oauth token", "GH_OAUTH", ghOAuthToken, true},
	{"github user-to-server token", "GH_USER_TOKEN", ghUserToken, true},
	{"github server-to-server token", "GH_SERVER_TOKEN", ghServerToken, true},
	{"aws access key id", "AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE", true},
	{"aws temporary access key id", "AWS_ACCESS_KEY_ID", "ASIAY34FZKBOKMUTVV7A", true},
	{"gitlab pat", "GITLAB_TOKEN", gitlabPAT, true},
	{"digitalocean token", "DIGITALOCEAN_TOKEN", digitalOceanPAT, true},
	{"jwt", "API_TOKEN", jwt, true},

	// Documented false negative: an inline credential in a URL's userinfo.
	{"url with userinfo", "NOTES", "https://user:s3cr3tpassword@mcp.example.com/v1", true},

	// Documented false negatives: the key name was not on the list, and the
	// key name is the only thing that lets a prefix-less blob be flagged.
	{"aws secret access key", "AWS_SECRET_ACCESS_KEY", "wJalrUXnFMI/K7MDENG/bPxRfiCYEXAMPLEKEY", true},
	{"legacy 40-hex pat under auth", "GH_AUTH", "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", true},
	{"blob under credentials", "CREDENTIALS", "wJalrUXnFMI+K7MDENGbPxRfiCYEXAMPLEKEY0", true},
	{"blob under cookie", "COOKIE", "AbCd1234EfGh5678IjKl9012MnOp3456", true},
	{"blob under passwd", "PASSWD", "S3cur3P4ssw0rdWithEnoughLengthToLook32", true},
	{"blob under pwd", "DB_PWD", "aB3xQ9mZ7pL2vN8kR5tY1wD4fH6jC0sE", true},

	// Must stay quiet: the shape friday steers users toward.
	{"env reference", "GITHUB_TOKEN", "${GITHUB_TOKEN}", false},
	{"bearer-wrapped env reference", "Authorization", "Bearer ${LINEAR_KEY}", false},
	{"short value", "X-Api-Key", "short", false},

	// Must stay quiet: documented false positives.
	{"long posix path", "MODEL_KEY_DIR", "/home/user/projects/friday/internal/render", false},
	{"provider-qualified model", "MODEL_KEY", "anthropic/claude-sonnet-4-5-20250929", false},
	{"key substring in a plain name", "MONKEY_CONFIG", "aB3xQ9mZ7pL2vN8kR5tY1wD4fH6jC0sE", false},
	{"key prefix in a plain name", "KEYBOARD_LAYOUT", "aB3xQ9mZ7pL2vN8kR5tY1wD4fH6jC0sE", false},
	{"blob under an uninteresting key", "X-Request-Id", "aB3xQ9mZ7pL2vN8kR5tY1wD4fH6jC0sE", false},
	{"plain http url", "NOTES", "https://mcp.linear.app/sse", false},
	{"http url with a port", "NOTES", "http://localhost:8080/mcp", false},
}

func TestScanCredentialCorpus(t *testing.T) {
	for _, tc := range credentialCorpus {
		t.Run(tc.name, func(t *testing.T) {
			m := spec.MCP{Servers: map[string]spec.MCPServer{
				"srv": {Transport: spec.TransportStdio, Command: "npx", Env: map[string]string{tc.key: tc.value}},
			}}
			got := hasKey(Scan(m), "srv.env."+tc.key)
			if got != tc.want {
				t.Errorf("flagged = %v, want %v for key %q", got, tc.want, tc.key)
			}
		})
	}
}

// A finding must never carry the credential: a lint result is routinely
// pasted into an issue tracker or a CI log.
func TestFindingNeverEchoesTheValue(t *testing.T) {
	for _, tc := range credentialCorpus {
		if !tc.want {
			continue
		}
		m := spec.MCP{Servers: map[string]spec.MCPServer{
			"srv": {Transport: spec.TransportStdio, Command: "npx", Env: map[string]string{tc.key: tc.value}},
		}}
		for _, f := range Scan(m) {
			if strings.Contains(f.Key, tc.value) || strings.Contains(f.Message(), tc.value) {
				t.Errorf("%s: finding echoed the credential: %q", tc.name, f.Message())
			}
			if !strings.Contains(f.Message(), f.Key) {
				t.Errorf("%s: message %q does not name the key %q", tc.name, f.Message(), f.Key)
			}
		}
	}
}

func TestScanFlagsEnvReferenceNever(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Env: map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
	}}
	if got := Scan(m); len(got) != 0 {
		t.Errorf("env reference must never be flagged: %+v", got)
	}
}

func TestScanFlagsVendorPrefixRegardlessOfKeyName(t *testing.T) {
	// A vendor prefix is unambiguous on its own — it must not require a
	// sensitive-looking key name to be flagged.
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Env: map[string]string{"NOTES": openAIKey}},
	}}
	got := Scan(m)
	if !hasKey(got, "github.env.NOTES") {
		t.Errorf("got %+v, want github.env.NOTES flagged (vendor prefix alone is enough)", got)
	}
}

func TestScanFlagsVendorPrefixInArgs(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"github": {Transport: spec.TransportStdio, Command: "npx", Args: []string{"--token", openAIKey}},
	}}
	got := Scan(m)
	if !hasKey(got, "github.args[1]") {
		t.Errorf("got %+v, want github.args[1] flagged", got)
	}
	if hasKey(got, "github.args[0]") {
		t.Errorf("got %+v, want the bare --token flag left alone", got)
	}
}

// "Authorization: Bearer <token>" is the commonest shape a real credential
// takes in an MCP config; the pre-v1.0 detector missed it on both gates at
// once, and this test asserted that miss as desired behaviour.
func TestScanFlagsBearerTokenInHeaders(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"linear": {Transport: spec.TransportHTTP, URL: "https://mcp.linear.app", Headers: map[string]string{"Authorization": "Bearer " + openAIKey}},
	}}
	got := Scan(m)
	if !hasKey(got, "linear.headers.Authorization") {
		t.Errorf("got %+v, want linear.headers.Authorization flagged", got)
	}
}

func TestScanIgnoresBearerEnvReferenceInHeaders(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"linear": {Transport: spec.TransportHTTP, URL: "https://mcp.linear.app", Headers: map[string]string{"Authorization": "Bearer ${LINEAR_KEY}"}},
	}}
	if got := Scan(m); len(got) != 0 {
		t.Errorf("got %+v, want no findings — an env reference stays an env reference when wrapped", got)
	}
}

func TestScanFlagsUserinfoInURL(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"remote": {Transport: spec.TransportHTTP, URL: "https://user:s3cr3tpassword@mcp.example.com/v1"},
	}}
	got := Scan(m)
	if !hasKey(got, "remote.url") {
		t.Errorf("got %+v, want remote.url flagged (inline userinfo credential)", got)
	}
}

func TestScanIgnoresOrdinaryURL(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"remote": {Transport: spec.TransportHTTP, URL: "https://mcp.example.com/v1", Headers: map[string]string{"Authorization": "Bearer ${KEY}"}},
	}}
	if got := Scan(m); len(got) != 0 {
		t.Errorf("got %+v, want no findings for an ordinary URL", got)
	}
}

func TestScanFlagsCredentialInCommand(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"svc": {Transport: spec.TransportStdio, Command: openAIKey},
	}}
	got := Scan(m)
	if !hasKey(got, "svc.command") {
		t.Errorf("got %+v, want svc.command flagged", got)
	}
}

func TestScanFlagsRandomLookingValueUnderSensitiveKey(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"custom": {Transport: spec.TransportHTTP, URL: "https://example.com", Headers: map[string]string{
			"X-Api-Key": "aB3xQ9mZ7pL2vN8kR5tY1wD4fH6jC0sE",
		}},
	}}
	got := Scan(m)
	if !hasKey(got, "custom.headers.X-Api-Key") {
		t.Errorf("got %+v, want X-Api-Key flagged (32+ random chars under a key-like name)", got)
	}
}

func TestScanIgnoresShortValueUnderSensitiveKey(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"custom": {Transport: spec.TransportHTTP, URL: "https://example.com", Headers: map[string]string{
			"X-Api-Key": "short",
		}},
	}}
	if got := Scan(m); len(got) != 0 {
		t.Errorf("got %+v, want no findings (too short to be a real credential)", got)
	}
}

func TestScanIgnoresRandomLookingValueUnderPlainKey(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"custom": {Transport: spec.TransportHTTP, URL: "https://example.com", Headers: map[string]string{
			"X-Request-Id": "aB3xQ9mZ7pL2vN8kR5tY1wD4fH6jC0sE",
		}},
	}}
	if got := Scan(m); len(got) != 0 {
		t.Errorf("got %+v, want no findings (key name gives no reason to suspect a secret)", got)
	}
}

func TestScanFlagsPEMPrivateKey(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"svc": {Transport: spec.TransportStdio, Command: "run", Env: map[string]string{
			"SERVICE_ACCOUNT": "-----BEGIN PRIVATE KEY-----\nMIIEvQ...\n-----END PRIVATE KEY-----",
		}},
	}}
	got := Scan(m)
	if !hasKey(got, "svc.env.SERVICE_ACCOUNT") {
		t.Errorf("got %+v, want SERVICE_ACCOUNT flagged (PEM key block)", got)
	}
}

func TestScanEmptyValueNeverFlagged(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"svc": {Transport: spec.TransportStdio, Command: "run", Env: map[string]string{"API_KEY": ""}},
	}}
	if got := Scan(m); len(got) != 0 {
		t.Errorf("got %+v, want no findings for an empty value", got)
	}
}

func TestScanDeterministicOrder(t *testing.T) {
	m := spec.MCP{Servers: map[string]spec.MCPServer{
		"z-server": {Transport: spec.TransportStdio, Command: "a", Env: map[string]string{"TOKEN": openAIKey}},
		"a-server": {Transport: spec.TransportStdio, Command: "b", Env: map[string]string{"TOKEN": ghClassicPAT}},
	}}
	got := Scan(m)
	if len(got) != 2 || got[0].Key != "a-server.env.TOKEN" || got[1].Key != "z-server.env.TOKEN" {
		t.Errorf("got %+v, want server names in sorted order", got)
	}
}

func TestScanYAMLParsesAndScans(t *testing.T) {
	data := []byte("version: 1\nservers:\n  github:\n    transport: stdio\n    command: npx\n    env: {GITHUB_TOKEN: \"" + ghClassicPAT + "\"}\n")
	got := ScanYAML(data)
	if !hasKey(got, "github.env.GITHUB_TOKEN") {
		t.Errorf("got %+v, want github.env.GITHUB_TOKEN flagged", got)
	}
}

func TestScanYAMLIgnoresUnparseableContent(t *testing.T) {
	// A schema error belongs to the caller's own rule; reporting it twice,
	// once under a credential rule, would only mislead.
	if got := ScanYAML([]byte("servers:\n  x:\n    transport: carrier-pigeon\n")); got != nil {
		t.Errorf("got %+v, want no findings for content that does not parse", got)
	}
}

func TestScanAnyWalksNestedDocument(t *testing.T) {
	doc := map[string]any{
		"mcpServers": map[string]any{
			"linear": map[string]any{
				"headers": map[string]any{"Authorization": "Bearer " + ghClassicPAT},
				"args":    []any{"--stdio", openAIKey},
			},
		},
		"theme": "dark",
	}
	got := ScanAny("native/claude/.mcp.json", doc)
	if len(got) != 2 {
		t.Fatalf("got %+v, want exactly two findings", got)
	}
	if got[0].Key != "native/claude/.mcp.json.mcpServers.linear.args[1]" {
		t.Errorf("got[0] = %q", got[0].Key)
	}
	if got[1].Key != "native/claude/.mcp.json.mcpServers.linear.headers.Authorization" {
		t.Errorf("got[1] = %q", got[1].Key)
	}
}

func TestScanAnyIgnoresCleanDocument(t *testing.T) {
	doc := map[string]any{
		"statusline": map[string]any{"command": "~/.friday/bin/statusline.sh", "padding": 2},
		"env":        map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
		"paths":      []string{"/home/user/projects/friday/internal/render"},
	}
	if got := ScanAny("defaults.yaml", doc); got != nil {
		t.Errorf("got %+v, want no findings for a clean document", got)
	}
}
