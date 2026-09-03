package spec

import (
	"strings"
	"testing"
)

func TestParsePermissionsValid(t *testing.T) {
	p, err := ParsePermissions([]byte(`
version: 1
read:   { deny: ["**/.env", "**/.env.*"] }
write:  { allow: ["src/**"], ask: ["**"] }
exec:   { allow: ["git status", "npm run build"], deny: ["git push --force"] }
net:    { allow: ["docs.anthropic.com"], deny: ["*"] }
mcp:    { allow: ["github/*"], ask: ["*"] }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Read.Deny) != 2 || p.Read.Deny[0] != "**/.env" {
		t.Errorf("read.deny = %v", p.Read.Deny)
	}
	if len(p.Write.Allow) != 1 || len(p.Write.Ask) != 1 {
		t.Errorf("write = %+v", p.Write)
	}
	if len(p.Exec.Deny) != 1 || p.Exec.Deny[0] != "git push --force" {
		t.Errorf("exec.deny = %v", p.Exec.Deny)
	}
	if len(p.Net.Allow) != 1 || len(p.Net.Deny) != 1 {
		t.Errorf("net = %+v", p.Net)
	}
	if len(p.MCP.Allow) != 1 || len(p.MCP.Ask) != 1 {
		t.Errorf("mcp = %+v", p.MCP)
	}
}

func TestParsePermissionsVersionDefaultsAndRejects(t *testing.T) {
	p, err := ParsePermissions([]byte(""))
	if err != nil || p.Version != 1 {
		t.Fatalf("version default: p=%+v err=%v", p, err)
	}
	if _, err := ParsePermissions([]byte("version: 2\n")); err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestParsePermissionsRejectsUnknownAxis(t *testing.T) {
	_, err := ParsePermissions([]byte("networking: { allow: [\"*\"] }\n"))
	// Naming the offending key and its line is the whole UX of a typo; a
	// bare non-nil check would still pass against a generic wrapper.
	if err == nil || !strings.Contains(err.Error(), `unknown field "networking"`) || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("err = %v, want the key and line named", err)
	}
}

func TestParsePermissionsRejectsUnknownAction(t *testing.T) {
	_, err := ParsePermissions([]byte("read: { warn: [\"*\"] }\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown field "warn"`) {
		t.Errorf("err = %v, want the key named", err)
	}
	// The message is rendered into a single-line status Reason, and the Go
	// type the field decoded into is not something the user wrote.
	if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "spec.") {
		t.Errorf("err = %q, want one line and no Go type name", err)
	}
}

func TestParsePermissionsRejectsDuplicateKey(t *testing.T) {
	_, err := ParsePermissions([]byte("read:\n  deny: [\"a\"]\n  deny: [\"b\"]\n"))
	if err == nil || !strings.Contains(err.Error(), `"deny" already defined`) {
		t.Errorf("err = %v, want the duplicated key named", err)
	}
}

func TestParsePermissionsRejectsWrongScalarType(t *testing.T) {
	_, err := ParsePermissions([]byte("version: \"one\"\n"))
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("err = %v", err)
	}
}

// yaml's Decoder is a document stream: without a guard, everything after a
// `---` separator is decoded into nothing and dropped without a word.
func TestParsePermissionsRejectsSecondDocument(t *testing.T) {
	_, err := ParsePermissions([]byte("read:\n  deny: [\"a\"]\n---\nwrite:\n  deny: [\"b\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Errorf("err = %v, want a multi-document refusal", err)
	}
}

func TestParsePermissionsErrorDoesNotNameTheFile(t *testing.T) {
	// The caller knows which of several source roots the content came from
	// and prefixes the path itself; prefixing it here too doubled it.
	_, err := ParsePermissions([]byte("read: { deny: [\"\"] }\n"))
	if err == nil || strings.Contains(err.Error(), "permissions.yaml") {
		t.Errorf("err = %v, want no filename prefix", err)
	}
}

func TestParsePermissionsRejectsEmptyPattern(t *testing.T) {
	_, err := ParsePermissions([]byte("read: { deny: [\"\"] }\n"))
	if err == nil || !strings.Contains(err.Error(), "empty pattern") {
		t.Errorf("err = %v", err)
	}
}

func TestPermissionsAxesOrder(t *testing.T) {
	p := &Permissions{
		Read:  PermissionAxis{Allow: []string{"r"}},
		Write: PermissionAxis{Allow: []string{"w"}},
		Exec:  PermissionAxis{Allow: []string{"e"}},
		Net:   PermissionAxis{Allow: []string{"n"}},
		MCP:   PermissionAxis{Allow: []string{"m"}},
	}
	axes := p.Axes()
	wantNames := []string{AxisRead, AxisWrite, AxisExec, AxisNet, AxisMCP}
	if len(axes) != len(wantNames) {
		t.Fatalf("got %d axes, want %d", len(axes), len(wantNames))
	}
	for i, want := range wantNames {
		if axes[i].Name != want {
			t.Errorf("Axes()[%d].Name = %q, want %q", i, axes[i].Name, want)
		}
	}
}
