package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// fakeQuerier is an in-memory Querier for handler tests (no database).
type fakeQuerier struct {
	overview  *Overview
	ts        *Timeseries
	employers []EmployerHealth
	funds     []FundAUM
	err       error // injected failure (e.g. errNotReady)
}

func (f *fakeQuerier) Overview(context.Context) (*Overview, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.overview, nil
}

func (f *fakeQuerier) Timeseries(_ context.Context, metric string, _, _ *time.Time) (*Timeseries, error) {
	if f.err != nil {
		return nil, f.err
	}
	if metric != "member_growth" {
		return nil, errUnknownMetric
	}
	return f.ts, nil
}

func (f *fakeQuerier) EmployerHealth(context.Context) ([]EmployerHealth, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.employers, nil
}

func (f *fakeQuerier) FundAUM(context.Context) ([]FundAUM, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.funds, nil
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr
}

func TestFailClosedWhenQuerierNil(t *testing.T) {
	h := routes(nil)
	for _, p := range []string{
		"/v1/analytics/overview",
		"/v1/analytics/timeseries?metric=member_growth",
		"/v1/analytics/employers/health",
		"/v1/analytics/funds/aum",
	} {
		rr := get(t, h, p)
		if rr.Code != 503 {
			t.Fatalf("%s: got %d, want 503", p, rr.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != "ANALYTICS_NOT_READY" {
			t.Fatalf("%s: code = %q", p, body["code"])
		}
	}
}

func TestOverview(t *testing.T) {
	h := routes(&fakeQuerier{overview: &Overview{
		TotalMembers: 3, TotalActiveMembers: 3, MembersAsOf: "2024-06-01",
		AumKobo: 30000, AumAsOf: "2024-06-01",
		LastMonth: "2024-05-01", LastMonthGrossKobo: 43000000, PrevMonthGrossKobo: 10000000,
		CaseSLAComplianceBps: 5000, CaseSLAMonth: "2024-06-01",
		KycFunnel: []FunnelStage{{Stage: "started", Sessions: 2}},
	}})
	rr := get(t, h, "/v1/analytics/overview")
	if rr.Code != 200 {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
	var o Overview
	if err := json.Unmarshal(rr.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	if o.TotalMembers != 3 || o.AumKobo != 30000 || o.LastMonthGrossKobo != 43000000 {
		t.Fatalf("unexpected overview: %+v", o)
	}
	if len(o.KycFunnel) != 1 || o.KycFunnel[0].Sessions != 2 {
		t.Fatalf("funnel: %+v", o.KycFunnel)
	}
}

func TestTimeseries(t *testing.T) {
	h := routes(&fakeQuerier{ts: &Timeseries{Metric: "member_growth",
		Points: []TSPoint{{Label: "2024-01-05", Value: 1, Value2: 1}}}})

	rr := get(t, h, "/v1/analytics/timeseries?metric=member_growth&from=2024-01-01&to=2024-12-31")
	if rr.Code != 200 {
		t.Fatalf("got %d: %s", rr.Code, rr.Body)
	}
	var tsResp Timeseries
	if err := json.Unmarshal(rr.Body.Bytes(), &tsResp); err != nil {
		t.Fatal(err)
	}
	if len(tsResp.Points) != 1 || tsResp.Points[0].Value != 1 {
		t.Fatalf("points: %+v", tsResp.Points)
	}

	rr = get(t, h, "/v1/analytics/timeseries?metric=bogus")
	if rr.Code != 400 {
		t.Fatalf("unknown metric: got %d, want 400", rr.Code)
	}
}

func TestEmployerHealthAndFundAUM(t *testing.T) {
	h := routes(&fakeQuerier{
		employers: []EmployerHealth{
			{EmployerID: "emp-1", EmployerName: "Acme", RemittanceHealth: "HEALTHY", TotalRemittedKobo: 43000000},
			{EmployerID: "emp-2", EmployerName: "Ghost", RemittanceHealth: "DEFAULTING"},
		},
		funds: []FundAUM{
			{FundID: "FUND_II", FundName: "Fund II", AsOf: "2024-06-01", PriceKobo: 16000, UnitsRaw: 200000000, AumKobo: 32000},
			{FundID: "FUND_IV", FundName: "Fund IV", AsOf: "2024-06-01", PriceKobo: 10000, UnitsRaw: 100000000, AumKobo: 10000},
		},
	})

	rr := get(t, h, "/v1/analytics/employers/health")
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
	var eh struct {
		Employers []EmployerHealth `json:"employers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &eh); err != nil {
		t.Fatal(err)
	}
	if len(eh.Employers) != 2 || eh.Employers[1].RemittanceHealth != "DEFAULTING" {
		t.Fatalf("employers: %+v", eh.Employers)
	}

	rr = get(t, h, "/v1/analytics/funds/aum")
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
	var fa struct {
		Funds        []FundAUM `json:"funds"`
		TotalAumKobo int64     `json:"total_aum_kobo"`
		AsOf         string    `json:"as_of"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &fa); err != nil {
		t.Fatal(err)
	}
	if fa.TotalAumKobo != 42000 || fa.AsOf != "2024-06-01" || len(fa.Funds) != 2 {
		t.Fatalf("aum: %+v", fa)
	}
}

func TestSchemaMissingMapsTo503(t *testing.T) {
	h := routes(&fakeQuerier{err: errNotReady})
	rr := get(t, h, "/v1/analytics/overview")
	if rr.Code != 503 {
		t.Fatalf("got %d, want 503", rr.Code)
	}
}

func TestQueryFailureMapsTo500(t *testing.T) {
	h := routes(&fakeQuerier{err: errors.New("boom")})
	rr := get(t, h, "/v1/analytics/overview")
	if rr.Code != 500 {
		t.Fatalf("got %d, want 500", rr.Code)
	}
}

func TestHealthz(t *testing.T) {
	rr := get(t, routes(nil), "/healthz")
	if rr.Code != 200 {
		t.Fatalf("healthz: got %d", rr.Code)
	}
}

func TestCDCStatusFailClosed(t *testing.T) {
	// nil querier => 503 like every other endpoint, even though the
	// payload itself is file-based.
	rr := get(t, routes(nil), "/v1/analytics/cdc-status")
	if rr.Code != 503 {
		t.Fatalf("got %d, want 503", rr.Code)
	}
}

func TestCDCStatusDisabledWithoutEnv(t *testing.T) {
	t.Setenv("CDC_WATERMARK_PATH", "")
	h := routes(&fakeQuerier{})
	rr := get(t, h, "/v1/analytics/cdc-status")
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["cdc_enabled"] != false || body["mode"] != "full-refresh" {
		t.Fatalf("unexpected: %v", body)
	}
}

func TestCDCStatusNoWatermarkYet(t *testing.T) {
	t.Setenv("CDC_WATERMARK_PATH", t.TempDir()+"/missing.json")
	h := routes(&fakeQuerier{})
	rr := get(t, h, "/v1/analytics/cdc-status")
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["cdc_enabled"] != true || body["status"] != "no_watermark" {
		t.Fatalf("unexpected: %v", body)
	}
}

func TestCDCStatusWithWatermark(t *testing.T) {
	ts := time.Now().UTC().Add(-5 * time.Second).Format(time.RFC3339)
	wm := `{"version":1,"seq":42,"lsn":777,"updated_at":"` + ts + `",
	  "offsets":{"pfaengine.public.member":{"0":12}},
	  "tables":{"member":{"events":40,"last_seq":42,"last_op":"u",
	    "last_applied_at":"` + ts + `"}}}`
	path := t.TempDir() + "/cdc_watermark.json"
	if err := os.WriteFile(path, []byte(wm), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CDC_WATERMARK_PATH", path)
	h := routes(&fakeQuerier{})
	rr := get(t, h, "/v1/analytics/cdc-status")
	if rr.Code != 200 {
		t.Fatalf("got %d", rr.Code)
	}
	var body struct {
		CDCEnabled bool    `json:"cdc_enabled"`
		Status     string  `json:"status"`
		Seq        int64   `json:"seq"`
		LagSeconds float64 `json:"lag_seconds"`
		Tables     map[string]struct {
			Events     int64   `json:"events"`
			LagSeconds float64 `json:"lag_seconds"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.CDCEnabled || body.Status != "ok" || body.Seq != 42 {
		t.Fatalf("unexpected: %+v", body)
	}
	if body.LagSeconds < 4 || body.Tables["member"].Events != 40 {
		t.Fatalf("lag/tables: %+v", body)
	}
}
