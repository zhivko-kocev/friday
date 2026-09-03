package spec

import (
	"strings"
	"testing"
)

func TestAgentDocValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		doc     AgentDoc
		wantErr bool
	}{
		{"instructions present", AgentDoc{Name: "reviewer", Description: "reviews", Body: "Review the diff."}, false},
		{"frontmatter only", AgentDoc{Name: "reviewer", Description: "reviews"}, true},
		{"whitespace-only body", AgentDoc{Name: "reviewer", Body: "\n\n  \n"}, true},
		{"no frontmatter, body only", AgentDoc{Name: "reviewer", Body: "Review the diff."}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.doc.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "no instructions") {
				t.Errorf("err = %v, want the empty body named", err)
			}
		})
	}
}
