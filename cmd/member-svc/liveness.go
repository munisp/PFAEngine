package main

// Gap 9: challenge-response (active) liveness handlers. These sit alongside
// the Tranche-22 eKYC session handlers: when tenant policy demands high
// assurance (EKYC_REQUIRE_CHALLENGE or a per-session challenge), the client
// performs the issued actions live on camera and posts the per-action
// results back. Fail-closed throughout: no engine -> 503
// LIVENESS_UNCONFIGURED; nonce/expiry/replay errors -> 400; no biometric
// material is accepted, stored or logged on these routes.

import (
	"errors"
	"net/http"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/ekyc"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/liveness"
)

var errLivenessUnconfigured = errors.New("liveness challenge engine not configured (set EKYC_REQUIRE_CHALLENGE/LIVENESS_THRESHOLD and SMILE_ID_* for vendor-backed verification)")

// ekycChallengeError maps engine errors onto HTTP responses.
func ekycChallengeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ekyc.ErrSessionNotFound):
		httpsrv.WriteError(w, http.StatusNotFound, "NOT_FOUND", err)
	case errors.Is(err, ekyc.ErrLivenessNotConfigured):
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "LIVENESS_UNCONFIGURED", err)
	case errors.Is(err, liveness.ErrChallengeExpired):
		httpsrv.WriteError(w, http.StatusGone, "CHALLENGE_EXPIRED", err)
	case errors.Is(err, liveness.ErrNonceMismatch), errors.Is(err, liveness.ErrChallengeUsed),
		errors.Is(err, liveness.ErrChallengeNotFound), errors.Is(err, liveness.ErrIncompleteResponse):
		httpsrv.WriteError(w, http.StatusBadRequest, "CHALLENGE_INVALID", err)
	default:
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
	}
}

// POST /api/v1/ekyc/sessions/{id}/challenge — issue a randomized,
// nonce-bound active-liveness challenge (2-3 actions, 120s TTL).
func (d *deps) ekycIssueChallenge(w http.ResponseWriter, r *http.Request) {
	if d.liveness == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "LIVENESS_UNCONFIGURED", errLivenessUnconfigured)
		return
	}
	v, err := d.ekyc.IssueChallenge(httpsrv.TenantOf(r), r.PathValue("id"), actorOf(r))
	if err != nil {
		ekycChallengeError(w, err)
		return
	}
	if d.audit != nil {
		d.audit.Append(audit.Record{TenantID: httpsrv.TenantOf(r), Actor: actorOf(r),
			Action: "EKYC_CHALLENGE_ISSUED", EntityType: "ekyc.session", EntityID: v.SessionID,
			After: map[string]any{"challenge_id": v.ID, "actions": len(v.Actions), "expires_at": v.ExpiresAt}})
	}
	httpsrv.WriteJSON(w, 201, v)
}

// POST /api/v1/ekyc/sessions/{id}/challenge/respond — submit per-action
// results bound to the challenge nonce. The session gains LivenessPassed on
// success or a challenge_failed risk flag on failure.
func (d *deps) ekycRespondChallenge(w http.ResponseWriter, r *http.Request) {
	if d.liveness == nil {
		httpsrv.WriteError(w, http.StatusServiceUnavailable, "LIVENESS_UNCONFIGURED", errLivenessUnconfigured)
		return
	}
	var in struct {
		ChallengeID string                  `json:"challenge_id"`
		Nonce       string                  `json:"nonce"`
		Results     []liveness.ActionResult `json:"results"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	if in.ChallengeID == "" || in.Nonce == "" {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", errors.New("challenge_id and nonce are required"))
		return
	}
	s, err := d.ekyc.SubmitChallengeResponse(httpsrv.TenantOf(r), r.PathValue("id"),
		in.ChallengeID, in.Nonce, in.Results, actorOf(r))
	if err != nil {
		ekycChallengeError(w, err)
		return
	}
	if d.audit != nil {
		d.audit.Append(audit.Record{TenantID: httpsrv.TenantOf(r), Actor: actorOf(r),
			Action: "EKYC_CHALLENGE_RESPONDED", EntityType: "ekyc.session", EntityID: s.ID,
			After: map[string]any{"challenge_id": s.ChallengeID, "passed": s.ChallengePassed, "score": s.ChallengeScore}})
	}
	httpsrv.WriteJSON(w, 200, s)
}
