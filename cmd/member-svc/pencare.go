package main

// Tranche 16a: PenCare retiree-healthcare API surface (Pension Industry
// Healthcare Plan for Retirees, launched Nov 2025 with the MPG).
// Handlers operate on the in-memory pencare.Engine and write through to
// Postgres (store.PenCareRepo) when DATABASE_URL is set. The engine's
// floor is sourced from the MPG engine, keeping PenCare low-income
// eligibility in lockstep with the ₦52,500 minimum pension.

import (
	"net/http"
	"strings"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/money"
	"github.com/munisp/pfaengine/internal/pencare"
)

// pencareFund returns the PenCare fund headline: floor, coverage counts
// and per-source aggregates (industry pool, PFA, PenCom, donor).
func (d *deps) pencareFund(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if d.pencareRepo != nil {
		agg, err := d.pencareRepo.FundAggregates(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_READ_FAILED", err)
			return
		}
		ens, err := d.pencareRepo.Enrollees(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_READ_FAILED", err)
			return
		}
		active := 0
		for _, en := range ens {
			if en.Status == pencare.EnrolleeActive {
				active++
			}
		}
		httpsrv.WriteJSON(w, 200, map[string]any{
			"floor_kobo": d.pencare.Floor(), "active_enrollees": active,
			"aggregates_kobo": agg,
		})
		return
	}
	n, cards, prem := d.pencare.Coverage(tenant)
	httpsrv.WriteJSON(w, 200, map[string]any{
		"floor_kobo": d.pencare.Floor(), "active_enrollees": n,
		"active_cards": cards, "premiums_paid_kobo": prem,
		"aggregates_kobo": d.pencare.FundAggregates(tenant),
	})
}

// createPenCareContribution books a receipt into the PenCare fund
// (INDUSTRY / PFA / PENCOM / DONOR). WORM.
func (d *deps) createPenCareContribution(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source         string     `json:"source"`
		ContributorRef string     `json:"contributor_ref"`
		AmountKobo     money.Kobo `json:"amount_kobo"`
		Period         string     `json:"period"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	fc, err := d.pencare.RecordFundContribution(tenant, pencare.FundSource(strings.ToUpper(in.Source)),
		strings.TrimSpace(in.ContributorRef), in.AmountKobo, strings.TrimSpace(in.Period), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PENCARE_REJECTED", err)
		return
	}
	if d.pencareRepo != nil {
		if err := d.pencareRepo.InsertFundContribution(r.Context(), fc); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "pencare.fund.recorded", EntityType: "pencare_fund_contribution", EntityID: fc.ID, After: map[string]any{"contribution": fc}})
	httpsrv.WriteJSON(w, 201, fc)
}

// listPenCareContributions returns the PenCare fund ledger.
func (d *deps) listPenCareContributions(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if d.pencareRepo != nil {
		fcs, err := d.pencareRepo.FundContributions(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"contributions": fcs})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"contributions": d.pencare.FundContributions(tenant)})
}

// enrollPenCare screens and registers a low-income retiree (monthly
// pension strictly below the MPG floor).
func (d *deps) enrollPenCare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RSAID              string     `json:"rsa_id"`
		MonthlyPensionKobo money.Kobo `json:"monthly_pension_kobo"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	en, err := d.pencare.Enroll(tenant, strings.TrimSpace(in.RSAID), in.MonthlyPensionKobo, time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PENCARE_NOT_ELIGIBLE", err)
		return
	}
	if d.pencareRepo != nil {
		if err := d.pencareRepo.InsertEnrollee(r.Context(), en); err != nil {
			// Re-enrolment is idempotent in memory; a UNIQUE violation in
			// Postgres is the same case — treat as already enrolled.
			if strings.Contains(err.Error(), "duplicate key") {
				httpsrv.WriteJSON(w, 200, en)
				return
			}
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "pencare.enrollee.registered", EntityType: "pencare_enrollee", EntityID: en.ID, After: map[string]any{"enrollee": en}})
	httpsrv.WriteJSON(w, 201, en)
}

// listPenCareEnrollees returns the tenant's PenCare enrollees.
func (d *deps) listPenCareEnrollees(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	if d.pencareRepo != nil {
		ens, err := d.pencareRepo.Enrollees(r.Context(), tenant)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"enrollees": ens})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"enrollees": d.pencare.Enrollees(tenant)})
}

// requestPenCareCard records a retiree's card request ("demand on their
// card at the PFA") and optionally issues/activates it in one call.
func (d *deps) requestPenCareCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EnrolleeID string `json:"enrollee_id"`
		CardNumber string `json:"card_number"` // when set, card is issued+activated immediately
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	c, err := d.pencare.RequestCard(tenant, strings.TrimSpace(in.EnrolleeID), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PENCARE_CARD_REJECTED", err)
		return
	}
	if in.CardNumber != "" {
		if c, err = d.pencare.IssueCard(tenant, c.ID, strings.TrimSpace(in.CardNumber), time.Now()); err != nil {
			httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PENCARE_CARD_REJECTED", err)
			return
		}
		if c, err = d.pencare.ActivateCard(tenant, c.ID); err != nil {
			httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PENCARE_CARD_REJECTED", err)
			return
		}
	}
	if d.pencareRepo != nil {
		c0 := c
		c0.Status = pencare.CardRequested
		c0.CardNumber = ""
		c0.IssuedAt = nil
		if err := d.pencareRepo.InsertCard(r.Context(), c0); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_PERSIST_FAILED", err)
			return
		}
		if in.CardNumber != "" {
			if err := d.pencareRepo.UpdateCard(r.Context(), c); err != nil {
				httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_PERSIST_FAILED", err)
				return
			}
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "pencare.card.requested", EntityType: "pencare_card", EntityID: c.ID, After: map[string]any{"card": c}})
	httpsrv.WriteJSON(w, 201, c)
}

// listPenCareCards lists cards, optionally ?enrollee_id= filtered.
func (d *deps) listPenCareCards(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	enrolleeID := r.URL.Query().Get("enrollee_id")
	if d.pencareRepo != nil {
		cs, err := d.pencareRepo.Cards(r.Context(), tenant, enrolleeID)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"cards": cs})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"cards": d.pencare.Cards(tenant, enrolleeID)})
}

// payPenCarePremium books an insurer premium payment for an enrollee. WORM.
func (d *deps) payPenCarePremium(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EnrolleeID string     `json:"enrollee_id"`
		InsurerRef string     `json:"insurer_ref"`
		AmountKobo money.Kobo `json:"amount_kobo"`
		Period     string     `json:"period"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)
	p, err := d.pencare.RecordPremium(tenant, strings.TrimSpace(in.EnrolleeID),
		strings.TrimSpace(in.InsurerRef), in.AmountKobo, strings.TrimSpace(in.Period), time.Now())
	if err != nil {
		httpsrv.WriteError(w, http.StatusUnprocessableEntity, "PENCARE_PREMIUM_REJECTED", err)
		return
	}
	if d.pencareRepo != nil {
		if err := d.pencareRepo.InsertPremium(r.Context(), p); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_PERSIST_FAILED", err)
			return
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc", Action: "pencare.premium.paid", EntityType: "pencare_premium", EntityID: p.ID, After: map[string]any{"premium": p}})
	httpsrv.WriteJSON(w, 201, p)
}

// listPenCarePremiums lists premium payments, optionally ?enrollee_id=.
func (d *deps) listPenCarePremiums(w http.ResponseWriter, r *http.Request) {
	tenant := httpsrv.TenantOf(r)
	enrolleeID := r.URL.Query().Get("enrollee_id")
	if d.pencareRepo != nil {
		ps, err := d.pencareRepo.Premiums(r.Context(), tenant, enrolleeID)
		if err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "PENCARE_READ_FAILED", err)
			return
		}
		httpsrv.WriteJSON(w, 200, map[string]any{"premiums": ps})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"premiums": d.pencare.Premiums(tenant, enrolleeID)})
}
