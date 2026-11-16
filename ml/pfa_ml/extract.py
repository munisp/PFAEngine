"""Production Postgres -> lakehouse extraction.

Pulls real platform data (contributions, benefits, transfers, fraud cases)
into parquet files that feed the training pipeline — this closes the
"no production DB -> training" gap. When the platform DB is empty or
unreachable (dev), callers fall back to the synthetic generator, and
continuous.py labels the run `data_source: synthetic`.

Parquet layout (lakehouse zone `training/`):
  transactions.parquet  — one row per ledger-visible financial event
  members.parquet       — member dimension
Requires: pip install pyarrow psycopg2-binary
"""
from __future__ import annotations

import json
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

EXTRACTION_SQL = {
    # Contribution events are the production financial stream. The prod
    # schema stores no channel/geo on contributions (collected via employer
    # remittance files) — those feature columns are NULL in production
    # extraction and the encoder treats them as the "unknown" bucket.
    "transactions": """
        SELECT id AS txn_id, rsa_pin AS member_id, 'contribution' AS kind,
               gross_kobo AS amount_kobo, NULL::text AS channel,
               NULL::text AS state,
               EXTRACT(EPOCH FROM created_at)::bigint AS ts
        FROM contribution
        WHERE tenant_id = %(tenant)s
        ORDER BY created_at
    """,
    "members": """
        SELECT a.id AS member_id, a.pin AS rsa_pin, a.fund_id, a.status
        FROM rsa_account a
        WHERE a.tenant_id = %(tenant)s
    """,
}


def extract(dsn: str, tenant: str, out_dir: str | Path) -> dict:
    """Extract production data to parquet. Raises on connection failure —
    callers (continuous.py) decide whether to fall back to synthetic."""
    import psycopg2

    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    counts = {}
    conn = psycopg2.connect(dsn)
    try:
        for name, sql in EXTRACTION_SQL.items():
            with conn.cursor() as cur:
                cur.execute(sql, {"tenant": tenant})
                rows = cur.fetchall()
                cols = [d.name for d in cur.description]
            table = pa.table({c: [r[i] for r in rows]
                              for i, c in enumerate(cols)})
            pq.write_table(table, out / f"{name}.parquet")
            counts[name] = len(rows)
    finally:
        conn.close()
    (out / "extract_manifest.json").write_text(json.dumps(
        {"tenant": tenant, "counts": counts, "source": "postgres"}))
    return counts
