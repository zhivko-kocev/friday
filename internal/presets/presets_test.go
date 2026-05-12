package presets

import (
	"reflect"
	"testing"
)

func TestGetReturnsDeepCopy(t *testing.T) {
	p1, ok := Get("claude")
	if !ok {
		t.Fatal("claude preset missing")
	}
	if len(p1.Rules) == 0 {
		t.Fatal("claude has no rules — registry corrupt")
	}
	// Mutate the returned rule's From slice — must not affect a fresh Get.
	p1.Rules[0].From[0] = "MUTATED"
	p2, _ := Get("claude")
	if reflect.DeepEqual(p1.Rules[0].From, p2.Rules[0].From) {
		t.Errorf("registry leaked: mutating Get's result reflected on next Get")
	}
}

func TestNamesIsSortedAndComplete(t *testing.T) {
	names := Names()
	want := []string{"claude", "copilot", "cursor", "opencode"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Names() = %v, want %v", names, want)
	}
}

func TestAllAdaptersHasEveryPreset(t *testing.T) {
	all := AllAdapters()
	for _, n := range Names() {
		if _, ok := all[n]; !ok {
			t.Errorf("AllAdapters missing %s", n)
		}
	}
}
