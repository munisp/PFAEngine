package main

// Tranche 22: next-generation eKYC/KYB handlers. eKYC sessions run the
// Ballerine-style pipeline (document intelligence -> NIN/BVN cross-check ->
// liveness + face match -> risk-scored decision) against the Python
// kyc-service sidecar (PaddleOCR + Docling + VLM, YuNet+SFace face match,
// MiniFASNet liveness ensemble). The sidecar is fail-closed: when it is
// unavailable, sessions are forced to manual review. KYB verifies CAC RC
// numbers, TINs, directors and UBOs (25% rule) with sanctions screening.

import (
	"net/http"

	"github.com/munisp/pfaengine/internal/docupload"
	"github.com/munisp/pfaengine/internal/httpsrv"
	"github.com/munisp/pfaengine/internal/kyb"
)

// ---- eKYC verification sessions ------------------------------------------------

func (d *deps) ekycStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MemberID string `json:"member_id"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	s := d.ekyc.Start(httpsrv.TenantOf(r), in.MemberID, actorOf(r))
	httpsrv.WriteJSON(w, 201, s)
}

func (d *deps) ekycGet(w http.ResponseWriter, r *http.Request) {
	s, err := d.ekyc.Get(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, 404, "NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, s)
}

func (d *deps) ekycList(w http.ResponseWriter, r *http.Request) {
	ss := d.ekyc.List(httpsrv.TenantOf(r), r.URL.Query().Get("state"))
	httpsrv.WriteJSON(w, 200, map[string]any{"sessions": ss})
}

func (d *deps) ekycSubmitDocument(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string `json:"kind"`
		Ref      string `json:"ref"`
		ImageB64 string `json:"image_b64"`
		UseVLM   bool   `json:"use_vlm"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	s, err := d.ekyc.SubmitDocument(httpsrv.TenantOf(r), r.PathValue("id"),
		docupload.Kind(in.Kind), in.Ref, in.ImageB64, in.UseVLM, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, s)
}

func (d *deps) ekycVerifyIdentity(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NIN   string `json:"nin"`
		BVN   string `json:"bvn"`
		Name  string `json:"name"`
		DOB   string `json:"dob"`
		Phone string `json:"phone"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	s, err := d.ekyc.VerifyIdentity(httpsrv.TenantOf(r), r.PathValue("id"),
		in.NIN, in.BVN, in.Name, in.DOB, in.Phone, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, s)
}

func (d *deps) ekycSubmitBiometric(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Frames []string `json:"frames"` // base64-encoded selfie frames
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	s, err := d.ekyc.SubmitBiometric(httpsrv.TenantOf(r), r.PathValue("id"), in.Frames, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, s)
}

func (d *deps) ekycDecide(w http.ResponseWriter, r *http.Request) {
	s, err := d.ekyc.Decide(httpsrv.TenantOf(r), r.PathValue("id"), actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, s)
}

func (d *deps) ekycOverride(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	s, err := d.ekyc.Override(httpsrv.TenantOf(r), r.PathValue("id"), in.Approve, actorOf(r), in.Note)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, s)
}

func (d *deps) ekycHealth(w http.ResponseWriter, r *http.Request) {
	h, err := d.ekycIntel.Health(r.Context())
	if err != nil {
		// fail-closed: report degraded, never 500 the health surface
		httpsrv.WriteJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	httpsrv.WriteJSON(w, 200, h)
}

// ---- KYB business verification -------------------------------------------------

func (d *deps) kybSubmit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EmployerID string       `json:"employer_id"`
		Name       string       `json:"name"`
		RCNumber   string       `json:"rc_number"`
		TIN        string       `json:"tin"`
		Directors  []kyb.Person `json:"directors"`
		UBOs       []kyb.UBO    `json:"ubos"`
		DocRefs    []string     `json:"doc_refs"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	b, err := d.kyb.Submit(httpsrv.TenantOf(r), in.EmployerID, in.Name, in.RCNumber,
		in.TIN, in.Directors, in.UBOs, in.DocRefs, actorOf(r))
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 201, b)
}

func (d *deps) kybGet(w http.ResponseWriter, r *http.Request) {
	b, err := d.kyb.Get(httpsrv.TenantOf(r), r.PathValue("id"))
	if err != nil {
		httpsrv.WriteError(w, 404, "NOT_FOUND", err)
		return
	}
	httpsrv.WriteJSON(w, 200, b)
}

func (d *deps) kybList(w http.ResponseWriter, r *http.Request) {
	bs := d.kyb.List(httpsrv.TenantOf(r), r.URL.Query().Get("status"))
	httpsrv.WriteJSON(w, 200, map[string]any{"businesses": bs})
}

func (d *deps) kybReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	b, err := d.kyb.Review(httpsrv.TenantOf(r), r.PathValue("id"), in.Approve, actorOf(r), in.Note)
	if err != nil {
		httpsrv.WriteError(w, 400, "BAD_REQUEST", err)
		return
	}
	httpsrv.WriteJSON(w, 200, b)
}
