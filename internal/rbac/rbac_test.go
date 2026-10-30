package rbac

import "testing"

func TestNewSetIgnoresUnknown(t *testing.T) {
	s := NewSet("doc.read", "doc.unknown", "tenant.manage")
	if !s.Has(DocRead) || !s.Has(TenantManage) {
		t.Fatalf("set does not contain known permissions: %v", s.List())
	}
	if len(s) != 2 {
		t.Fatalf("got %d permissions, want 2", len(s))
	}
}

func TestZeroSetHasNothing(t *testing.T) {
	var s Set
	for _, p := range All {
		if s.Has(p) {
			t.Fatalf("zero set has %s", p)
		}
	}
}

func TestAllHaveDescriptions(t *testing.T) {
	for _, p := range All {
		if p.Description() == "" {
			t.Errorf("permission %s has no description", p)
		}
	}
}
