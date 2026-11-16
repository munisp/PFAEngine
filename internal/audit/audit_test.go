package audit

import "testing"

func TestHashChainVerifies(t *testing.T) {
	s := NewStore()
	s.Append(Record{TenantID: "t1", Actor: "system", Action: "create", EntityType: "member", EntityID: "m1", After: map[string]string{"name": "Amara"}})
	s.Append(Record{TenantID: "t1", Actor: "system", Action: "update", EntityType: "member", EntityID: "m1", Before: map[string]string{"name": "Amara"}, After: map[string]string{"name": "Amara O."}})
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestTamperDetected(t *testing.T) {
	s := NewStore()
	s.Append(Record{TenantID: "t1", Actor: "a", Action: "create", EntityType: "x", EntityID: "1", After: 1})
	evs := s.Events()
	evs[0].After = []byte(`"tampered"`)
	_ = evs
	// Mutate via reflection is not possible (copy); simulate broken chain directly.
	s2 := NewStore()
	e := s2.Append(Record{TenantID: "t1", Actor: "a", Action: "create", EntityType: "x", EntityID: "1", After: 1})
	s2.Append(Record{TenantID: "t1", Actor: "a", Action: "create", EntityType: "x", EntityID: "2", After: 2})
	s2.events[0].Hash = "deadbeef"
	if err := s2.Verify(); err == nil {
		t.Fatal("expected chain break detection")
	}
	_ = e
}

func TestDualControlExport(t *testing.T) {
	s := NewStore()
	s.Append(Record{TenantID: "t1", Actor: "a", Action: "c", EntityType: "x", EntityID: "1"})
	if _, err := s.Export("t1", "staff1", "staff1"); err != ErrDualControl {
		t.Fatal("expected dual control error")
	}
	out, err := s.Export("t1", "staff1", "staff2")
	if err != nil || len(out) != 1 {
		t.Fatalf("export failed: %v", err)
	}
}
