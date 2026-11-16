package kyc

import "testing"

func svc() *Service {
	return NewService("test-salt",
		MockProvider{ProviderName: "smileid"},
		MockProvider{ProviderName: "dojah"},
	)
}

func TestNINTokenizedOnly(t *testing.T) {
	s := svc()
	rec, dup, err := s.VerifyNIN("t1", "p1", "12345678901", "Amara", "1992-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Token == "" || rec.Token == "12345678901" {
		t.Fatal("must store provider token, never raw NIN")
	}
	if dup != "" {
		t.Fatalf("unexpected dup %s", dup)
	}
}

func TestDuplicateDetectionAcrossTenants(t *testing.T) {
	s := svc()
	s.VerifyNIN("t1", "p1", "12345678901", "Amara", "1992-01-01")
	_, dup, _ := s.VerifyNIN("t2", "p2", "12345678901", "Amara", "1992-01-01")
	if dup != "p1" {
		t.Fatalf("expected duplicate of p1, got %q", dup)
	}
	_, dupB, _ := s.ValidateBVN("t1", "p1", "98765432109", "Amara", "1992-01-01", "0803")
	_, dupB2, _ := s.ValidateBVN("t2", "p3", "98765432109", "Amara Clone", "1992-01-01", "0803")
	if dupB != "" || dupB2 != "p1" {
		t.Fatalf("bvn dup detection failed: %q %q", dupB, dupB2)
	}
}

func TestLivenessThresholds(t *testing.T) {
	s := svc()
	s.VerifyNIN("t1", "p1", "12345678901", "A", "1992-01-01")
	c, _ := s.SubmitLiveness("t1", "p1", KindEnroll, "clean-selfie")
	if c.Status != StatusPassed || c.Score < 0.80 {
		t.Fatalf("expected pass, got %+v", c)
	}
	c2, _ := s.SubmitLiveness("t1", "p1", KindIAmAlive, "blur-selfie")
	if c2.Status != StatusManualReview {
		t.Fatalf("expected manual review, got %+v", c2)
	}
}

func TestLivenessRetryCap(t *testing.T) {
	s := svc()
	s.VerifyNIN("t1", "p9", "22222222222", "B", "1990-01-01")
	for i := 0; i < 3; i++ {
		c, _ := s.SubmitLiveness("t1", "p9", KindEnroll, "spoof-attack")
		if c.Status != StatusFailed {
			t.Fatalf("expected failed, got %+v", c)
		}
	}
	_, err := s.SubmitLiveness("t1", "p9", KindEnroll, "spoof-again")
	if err != ErrRetryCapExceeded {
		t.Fatalf("expected retry cap, got %v", err)
	}
}

func TestAggregatorFailoverQueues(t *testing.T) {
	s := NewService("salt", MockProvider{ProviderName: "down", FailAll: true})
	_, _, err := s.VerifyNIN("t1", "p1", "12345678901", "A", "1992-01-01")
	if err != ErrProviderUnavailable {
		t.Fatalf("expected unavailable, got %v", err)
	}
	if len(s.Queued()) != 1 {
		t.Fatal("expected queue-and-notify, never silent blocking")
	}
}

func TestConsentLifecycle(t *testing.T) {
	s := svc()
	c := s.RecordConsent("t1", "p1", "data-processing", "v2026-11", true)
	if !c.Granted {
		t.Fatal("consent not recorded")
	}
	if !s.WithdrawConsent("p1", "data-processing") {
		t.Fatal("withdrawal failed")
	}
	cs := s.Consents("p1")
	if cs[0].Granted {
		t.Fatal("withdrawal not persisted")
	}
}
