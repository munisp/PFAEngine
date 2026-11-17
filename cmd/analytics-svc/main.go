// analytics-svc: read-only JSON API over the Postgres `analytics` schema
// (gold lakehouse models exported by analytics/run.py). This service never
// computes business metrics itself and NEVER fabricates data: when
// DATABASE_URL is unset, Postgres is unreachable, or the analytics schema
// has not been populated yet, every endpoint returns
// 503 ANALYTICS_NOT_READY (fail-closed).
//
// Query layer is an interface (Querier) so handlers are unit-testable
// with an in-memory fake — no live database required in tests.
//
// Endpoints (all read-only GET):
//
//	/v1/analytics/overview              member counts, AUM, contribution
//	                                    velocity, case SLA %, KYC funnel
//	/v1/analytics/timeseries?metric=..&from=YYYY-MM-DD&to=YYYY-MM-DD
//	/v1/analytics/employers/health
//	/v1/analytics/funds/aum
//
// Money is integer kobo end-to-end (DA-03); presentation layers format
// naira. Percentages are integer basis points (10000 = 100%).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	_ "github.com/lib/pq"

	"github.com/munisp/pfaengine/internal/appx"
)

// errNotReady marks "analytics schema not populated / unreachable".
var errNotReady = errors.New("analytics schema not ready (run analytics/run.py)")

// ---- Response types ----

type FunnelStage struct {
	Stage    string `json:"stage"`
	Sessions int64  `json:"sessions"`
}

type Overview struct {
	TotalMembers         int64         `json:"total_members"`
	TotalActiveMembers   int64         `json:"total_active_members"`
	MembersAsOf          string        `json:"members_as_of"`
	AumKobo              int64         `json:"aum_kobo"`
	AumAsOf              string        `json:"aum_as_of"`
	LastMonth            string        `json:"last_contribution_month"`
	LastMonthGrossKobo   int64         `json:"last_month_gross_kobo"`
	PrevMonthGrossKobo   int64         `json:"prev_month_gross_kobo"`
	CaseSLAComplianceBps int64         `json:"case_sla_compliance_bps"`
	CaseSLAMonth         string        `json:"case_sla_month"`
	KycFunnel            []FunnelStage `json:"kyc_funnel"`
}

type TSPoint struct {
	Label  string `json:"label"`            // date or month (YYYY-MM-DD / YYYY-MM-01)
	Value  int64  `json:"value"`            // primary metric
	Value2 int64  `json:"value2,omitempty"` // secondary (e.g. cumulative, volume)
}

type Timeseries struct {
	Metric string    `json:"metric"`
	Points []TSPoint `json:"points"`
}

type EmployerHealth struct {
	EmployerID           string  `json:"employer_id"`
	EmployerName         string  `json:"employer_name"`
	ComplianceState      string  `json:"compliance_state"`
	SchedulesTotal       int64   `json:"schedules_total"`
	SchedulesPosted      int64   `json:"schedules_posted"`
	MonthsRemitted       int64   `json:"months_remitted"`
	TotalRemittedKobo    int64   `json:"total_remitted_kobo"`
	LastContributionDate *string `json:"last_contribution_date,omitempty"`
	RemittanceHealth     string  `json:"remittance_health"` // HEALTHY | WATCH | DEFAULTING
}

type FundAUM struct {
	FundID    string `json:"fund_id"`
	FundName  string `json:"fund_name"`
	AsOf      string `json:"as_of"`
	PriceKobo int64  `json:"price_kobo"`
	UnitsRaw  int64  `json:"units_raw"`
	AumKobo   int64  `json:"aum_kobo"`
}

// ---- Query layer (interface for testability) ----

type Querier interface {
	Overview(ctx context.Context) (*Overview, error)
	Timeseries(ctx context.Context, metric string, from, to *time.Time) (*Timeseries, error)
	EmployerHealth(ctx context.Context) ([]EmployerHealth, error)
	FundAUM(ctx context.Context) ([]FundAUM, error)
}

// ---- Postgres implementation ----

type pgQuerier struct{ db *sql.DB }

func newPGQuerier(ctx context.Context, dsn string) (*pgQuerier, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &pgQuerier{db: db}, nil
}

// classify maps schema-missing errors (42P01 undefined_table,
// 3F000 invalid_schema_name) onto errNotReady; everything else passes through.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var pqErr interface{ SQLState() string }
	if errors.As(err, &pqErr) {
		switch pqErr.SQLState() {
		case "42P01", "3F000", "42S02":
			return errNotReady
		}
	}
	return err
}

func (q *pgQuerier) Overview(ctx context.Context) (*Overview, error) {
	o := &Overview{KycFunnel: []FunnelStage{}}
	// Latest member totals.
	err := q.db.QueryRowContext(ctx,
		`SELECT total_members, total_active_members, dt::text
		   FROM analytics.gold_member_growth_daily ORDER BY dt DESC LIMIT 1`).
		Scan(&o.TotalMembers, &o.TotalActiveMembers, &o.MembersAsOf)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, classify(err)
	}
	// Latest AUM (sum across funds on the most recent NAV date).
	err = q.db.QueryRowContext(ctx,
		`SELECT dt::text, sum(aum_kobo)::bigint FROM analytics.gold_fund_aum_daily
		  GROUP BY dt ORDER BY dt DESC LIMIT 1`).Scan(&o.AumAsOf, &o.AumKobo)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, classify(err)
	}
	// Contribution velocity: latest two months of gross remittances.
	rows, err := q.db.QueryContext(ctx,
		`SELECT month::text, sum(gross_kobo)::bigint
		   FROM analytics.gold_contribution_monthly_by_employer
		  GROUP BY month ORDER BY month DESC LIMIT 2`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	for i := 0; rows.Next(); i++ {
		var month string
		var gross int64
		if err := rows.Scan(&month, &gross); err != nil {
			return nil, err
		}
		if i == 0 {
			o.LastMonth, o.LastMonthGrossKobo = month, gross
		} else {
			o.PrevMonthGrossKobo = gross
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}
	// Latest-month case SLA compliance (weighted across types/queues).
	err = q.db.QueryRowContext(ctx,
		`SELECT month::text,
		        round(10000.0 * (1.0 - sum(breached_cases)::numeric
		                         / nullif(sum(total_cases), 0)))::bigint
		   FROM analytics.gold_case_sla_compliance
		  WHERE month = (SELECT max(month) FROM analytics.gold_case_sla_compliance)
		  GROUP BY month`).Scan(&o.CaseSLAMonth, &o.CaseSLAComplianceBps)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, classify(err)
	}
	// KYC funnel stages.
	frows, err := q.db.QueryContext(ctx,
		`SELECT stage, sessions FROM analytics.gold_kyc_funnel ORDER BY stage_order`)
	if err != nil {
		return nil, classify(err)
	}
	defer frows.Close()
	for frows.Next() {
		var s FunnelStage
		if err := frows.Scan(&s.Stage, &s.Sessions); err != nil {
			return nil, err
		}
		o.KycFunnel = append(o.KycFunnel, s)
	}
	return o, classify(frows.Err())
}

// metrics whitelist — metric name -> gold query returning
// (label, value, value2). Prevents arbitrary SQL / table injection.
var metricQueries = map[string]string{
	"member_growth": `SELECT dt::text, new_members, total_members
		FROM analytics.gold_member_growth_daily
		WHERE ($1::date IS NULL OR dt >= $1::date) AND ($2::date IS NULL OR dt <= $2::date)
		ORDER BY dt`,
	"fund_aum": `SELECT dt::text, sum(aum_kobo)::bigint, count(DISTINCT fund_id)::bigint
		FROM analytics.gold_fund_aum_daily
		WHERE ($1::date IS NULL OR dt >= $1::date) AND ($2::date IS NULL OR dt <= $2::date)
		GROUP BY dt ORDER BY dt`,
	"contribution_monthly": `SELECT month::text, sum(gross_kobo)::bigint, sum(contributions)::bigint
		FROM analytics.gold_contribution_monthly_by_employer
		WHERE ($1::date IS NULL OR month >= $1::date) AND ($2::date IS NULL OR month <= $2::date)
		GROUP BY month ORDER BY month`,
	"case_sla": `SELECT month::text,
		round(10000.0 * (1.0 - sum(breached_cases)::numeric / nullif(sum(total_cases), 0)))::bigint,
		sum(total_cases)::bigint
		FROM analytics.gold_case_sla_compliance
		WHERE ($1::date IS NULL OR month >= $1::date) AND ($2::date IS NULL OR month <= $2::date)
		GROUP BY month ORDER BY month`,
}

var errUnknownMetric = errors.New("unknown metric (want one of: member_growth, fund_aum, contribution_monthly, case_sla)")

func (q *pgQuerier) Timeseries(ctx context.Context, metric string, from, to *time.Time) (*Timeseries, error) {
	sqlText, ok := metricQueries[metric]
	if !ok {
		return nil, errUnknownMetric
	}
	var fromArg, toArg any
	if from != nil {
		fromArg = from.Format("2006-01-02")
	}
	if to != nil {
		toArg = to.Format("2006-01-02")
	}
	rows, err := q.db.QueryContext(ctx, sqlText, fromArg, toArg)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out := &Timeseries{Metric: metric, Points: []TSPoint{}}
	for rows.Next() {
		var p TSPoint
		if err := rows.Scan(&p.Label, &p.Value, &p.Value2); err != nil {
			return nil, err
		}
		out.Points = append(out.Points, p)
	}
	return out, classify(rows.Err())
}

func (q *pgQuerier) EmployerHealth(ctx context.Context) ([]EmployerHealth, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT employer_id, employer_name, compliance_state, schedules_total,
		        schedules_posted, months_remitted, total_remitted_kobo,
		        last_contribution_date::text, remittance_health
		   FROM analytics.gold_employer_remittance_health
		  ORDER BY total_remitted_kobo DESC LIMIT 500`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out := []EmployerHealth{}
	for rows.Next() {
		var e EmployerHealth
		if err := rows.Scan(&e.EmployerID, &e.EmployerName, &e.ComplianceState,
			&e.SchedulesTotal, &e.SchedulesPosted, &e.MonthsRemitted,
			&e.TotalRemittedKobo, &e.LastContributionDate, &e.RemittanceHealth); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, classify(rows.Err())
}

func (q *pgQuerier) FundAUM(ctx context.Context) ([]FundAUM, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT DISTINCT ON (fund_id)
		        fund_id, fund_name, dt::text, price_kobo, units_raw, aum_kobo
		   FROM analytics.gold_fund_aum_daily
		  ORDER BY fund_id, dt DESC`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	out := []FundAUM{}
	for rows.Next() {
		var f FundAUM
		if err := rows.Scan(&f.FundID, &f.FundName, &f.AsOf, &f.PriceKobo, &f.UnitsRaw, &f.AumKobo); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, classify(rows.Err())
}

// ---- HTTP layer ----

func parseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// routes builds the mux. q may be nil (fail-closed: every endpoint
// answers 503 ANALYTICS_NOT_READY rather than inventing numbers).
func routes(q Querier) *http.ServeMux {
	mux := http.NewServeMux()
	appx.Health(mux, "analytics-svc")

	ready := func(w http.ResponseWriter) bool {
		if q == nil {
			appx.Err(w, 503, "ANALYTICS_NOT_READY", errNotReady)
			return false
		}
		return true
	}
	writeErr := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, errNotReady):
			appx.Err(w, 503, "ANALYTICS_NOT_READY", err)
		case errors.Is(err, errUnknownMetric):
			appx.Err(w, 400, "UNKNOWN_METRIC", err)
		default:
			appx.Err(w, 500, "QUERY_FAILED", err)
		}
	}

	mux.HandleFunc("GET /v1/analytics/overview", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		o, err := q.Overview(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		appx.JSON(w, 200, o)
	})

	mux.HandleFunc("GET /v1/analytics/timeseries", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		ts, err := q.Timeseries(r.Context(), r.URL.Query().Get("metric"),
			parseDate(r.URL.Query().Get("from")), parseDate(r.URL.Query().Get("to")))
		if err != nil {
			writeErr(w, err)
			return
		}
		appx.JSON(w, 200, ts)
	})

	mux.HandleFunc("GET /v1/analytics/employers/health", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		rows, err := q.EmployerHealth(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		appx.JSON(w, 200, map[string]any{"employers": rows})
	})

	mux.HandleFunc("GET /v1/analytics/funds/aum", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		funds, err := q.FundAUM(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		var total int64
		var asOf string
		for _, f := range funds {
			total += f.AumKobo
			if f.AsOf > asOf {
				asOf = f.AsOf
			}
		}
		appx.JSON(w, 200, map[string]any{"funds": funds, "total_aum_kobo": total, "as_of": asOf})
	})

	return mux
}

func main() {
	// Read-only analytics: data comes from the `analytics` schema populated
	// by analytics/run.py. Missing DSN or schema => 503s, never fake data.
	var q Querier
	if dsn := appx.Env("DATABASE_URL", ""); dsn != "" {
		pq, err := newPGQuerier(context.Background(), dsn)
		if err != nil {
			log.Printf("analytics-svc: DATABASE_URL set but connection failed (%v) — serving 503 ANALYTICS_NOT_READY", err)
		} else {
			q = pq
			log.Printf("analytics-svc: serving from Postgres analytics schema")
		}
	} else {
		log.Printf("analytics-svc: DATABASE_URL unset — serving 503 ANALYTICS_NOT_READY")
	}

	appx.Serve("analytics-svc", appx.Env("PORT", "8097"), routes(q))
}
