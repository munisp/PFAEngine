package main

// ML fraud scoring fusion: combines the deterministic rules engine
// (internal/fraud) with the PyTorch FraudFusion model served by
// ml/pfa_ml/serve.py. Fail-closed: when the ML service is unreachable or
// degraded, the rules score alone decides — the platform never loses fraud
// coverage because a sidecar is down.

import (
	"log"
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/audit"
	"github.com/munisp/pfaengine/internal/fraud"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/mlscore"
	"github.com/munisp/pfaengine/internal/money"
)

type mlScreenIn struct {
	MemberID    string `json:"member_id"`
	Kind        string `json:"kind"` // CONTRIBUTION | WITHDRAWAL | TRANSFER
	AmountKobo  int64  `json:"amount_kobo"`
	Channel     string `json:"channel"`
	StateCode   string `json:"state_code"`
	Day         int    `json:"day"`
	MinuteOfDay int    `json:"minute_of_day"`
	DeviceID    string `json:"device_id"`
}

func (d *deps) mlFraudScreen(w http.ResponseWriter, r *http.Request) {
	var in mlScreenIn
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err)
		return
	}
	tenant := httpsrv.TenantOf(r)

	// 1. rules engine (always runs — deterministic baseline)
	tx := fraud.Tx{
		TenantID: tenant, MemberID: in.MemberID, Kind: in.Kind,
		Amount: money.Kobo(in.AmountKobo), Channel: in.Channel,
		StateCode: in.StateCode, At: time.Now(),
	}
	rulesScore, signals, c := d.fraud.Score(tx)
	if c != nil && d.innovateRepo != nil {
		if err := d.innovateRepo.InsertFraudCase(r.Context(), *c); err != nil {
			httpsrv.WriteError(w, http.StatusInternalServerError, "FRAUD_PERSIST_FAILED", err)
			return
		}
	}

	// 2. ML fusion model (fail-closed)
	resp := map[string]any{
		"rules_score": rulesScore, "signals": signals, "case": c,
		"ml_available": false,
	}
	final := rulesScore
	if d.ml != nil {
		mlTxn := mlscore.Txn{
			TxnID: "screen-" + in.MemberID, MemberID: in.MemberID,
			Kind: lowerKind(in.Kind), AmountKobo: in.AmountKobo,
			Channel: lowerChan(in.Channel), State: in.StateCode,
			Day: in.Day, MinuteOfDay: in.MinuteOfDay, DeviceID: in.DeviceID,
		}
		if out, ok := d.ml.Score(r.Context(), in.MemberID, mlTxn, nil); ok {
			mlScore := int(out.Score * 100)
			resp["ml_available"] = true
			resp["ml_score"] = mlScore
			resp["ml_flagged"] = out.Flagged
			resp["ml_model"] = out.Model
			resp["ml_arm"] = out.Arm
			if mlScore > final {
				final = mlScore
			}
			if out.Flagged {
				// ML flagged above the MCMC-calibrated threshold; recommend
				// case review even when the rules engine stayed quiet.
				resp["ml_case_recommended"] = true
			}
		}
	}
	resp["final_score"] = final
	resp["flagged"] = final >= fraud.ScoreThreshold
	// Tranche 20: unified case engine — every flagged screen opens (or
	// merges into) a FRAUD ops case so nothing flagged goes uninvestigated.
	if c != nil && d.cases != nil {
		if _, _, err := d.cases.OpenFraudAlert(tenant, c.MemberID, c.ID, c.Score, c.Signals, false); err != nil {
			log.Printf("casemgmt: fraud auto-case failed: %v", err)
		}
	} else if c == nil && d.cases != nil && resp["ml_case_recommended"] == true {
		if _, _, err := d.cases.OpenFraudAlert(tenant, in.MemberID, "", final, append(signals, "ML_FLAGGED"), true); err != nil {
			log.Printf("casemgmt: ML auto-case failed: %v", err)
		}
	}
	d.audit.Append(audit.Record{TenantID: tenant, Actor: "member-svc",
		Action: "ml.fraud.screen", EntityType: "txn", EntityID: in.MemberID,
		After: resp})
	httpsrv.WriteJSON(w, 200, resp)
}

func (d *deps) mlHealth(w http.ResponseWriter, r *http.Request) {
	if d.ml == nil {
		httpsrv.WriteJSON(w, 200, map[string]any{"configured": false})
		return
	}
	v, err := d.ml.Health(r.Context())
	if err != nil {
		httpsrv.WriteJSON(w, 200, map[string]any{"configured": true, "degraded": true})
		return
	}
	httpsrv.WriteJSON(w, 200, map[string]any{"configured": true, "degraded": false, "champion": v})
}

func lowerKind(k string) string {
	switch k {
	case "CONTRIBUTION":
		return "contribution"
	case "WITHDRAWAL":
		return "withdrawal"
	case "TRANSFER":
		return "transfer"
	}
	return "inquiry"
}

func lowerChan(c string) string {
	switch c {
	case "USSD":
		return "ussd"
	case "WEB":
		return "web"
	case "MOBILE":
		return "mobile"
	case "BRANCH":
		return "branch"
	case "AGENT":
		return "agent"
	}
	return "web"
}
