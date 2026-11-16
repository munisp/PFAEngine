package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestLocaleSetGet(t *testing.T) {
	d := newTestDeps()
	h := d.handler()
	id := onboardMember(t, h)

	// Default locale is English.
	rr := do(t, h, http.MethodGet, "/api/v1/members/"+id+"/locale", "t1", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status = %d", rr.Code)
	}
	var out struct {
		MemberID string `json:"member_id"`
		Locale   string `json:"locale"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Locale != "en" {
		t.Fatalf("default locale = %q, want en", out.Locale)
	}

	// Every supported locale round-trips.
	for _, loc := range []string{"en", "yo", "ha", "ig", "pcm-NG"} {
		rr = do(t, h, http.MethodPut, "/api/v1/members/"+id+"/locale", "t1", `{"locale":"`+loc+`"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("put %s: status = %d (%s)", loc, rr.Code, rr.Body.String())
		}
		rr = do(t, h, http.MethodGet, "/api/v1/members/"+id+"/locale", "t1", "")
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if out.Locale != loc {
			t.Fatalf("locale = %q, want %q", out.Locale, loc)
		}
	}

	// The prefs store is the source of truth for channel services.
	if got := d.prefs.Get(context.Background(), "t1", id); string(got) != "pcm-NG" {
		t.Fatalf("prefs.Get = %q, want pcm-NG", got)
	}
}

func TestLocaleValidation(t *testing.T) {
	h := newTestDeps().handler()
	id := onboardMember(t, h)

	for _, body := range []string{
		`{"locale":"fr"}`,
		`{"locale":""}`,
		`{"locale":"pcm"}`, // only full tag pcm-NG is supported
	} {
		rr := do(t, h, http.MethodPut, "/api/v1/members/"+id+"/locale", "t1", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
	// Rejected values must not clobber the stored locale.
	rr := do(t, h, http.MethodGet, "/api/v1/members/"+id+"/locale", "t1", "")
	var out struct {
		Locale string `json:"locale"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Locale != "en" {
		t.Fatalf("locale after rejected PUTs = %q, want en", out.Locale)
	}
}

func TestLocaleUnknownMember(t *testing.T) {
	h := newTestDeps().handler()
	if rr := do(t, h, http.MethodPut, "/api/v1/members/prs-999/locale", "t1", `{"locale":"yo"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("put: status = %d, want 404", rr.Code)
	}
	if rr := do(t, h, http.MethodGet, "/api/v1/members/prs-999/locale", "t1", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("get: status = %d, want 404", rr.Code)
	}
}

// TestOnboardLinksPhone verifies the phone→member mapping used by the
// WhatsApp webhook locale resolution is populated at onboarding.
func TestOnboardLinksPhone(t *testing.T) {
	d := newTestDeps()
	h := d.handler()
	rr := onboard(t, h, `{"full_name":"Ada Lovelace","dob":"1990-01-01","nin":"12345678901","phone":"+2348030000000"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("onboard status = %d (%s)", rr.Code, rr.Body.String())
	}
	var created struct {
		MemberID string `json:"member_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	// WhatsApp senders arrive as bare digits.
	if got, ok := d.prefs.MemberForPhone(context.Background(), "t1", "2348030000000"); !ok || got != created.MemberID {
		t.Fatalf("MemberForPhone = %q, %v; want %q", got, ok, created.MemberID)
	}
}
