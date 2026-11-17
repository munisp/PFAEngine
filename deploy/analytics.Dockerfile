# Analytics lakehouse runner image: extract (Postgres -> bronze parquet),
# DuckDB silver/gold build, export gold -> Postgres `analytics` schema.
# One-shot job: docker compose run analytics-run (or cron — see
# docs/ANALYTICS.md refresh cadence).
FROM python:3.12-slim

WORKDIR /app
COPY analytics/requirements.txt /app/analytics/requirements.txt
RUN pip install --no-cache-dir -r analytics/requirements.txt
COPY analytics/ /app/analytics/
COPY db/migrations/ /app/db/migrations/
ENV PYTHONPATH=/app/analytics \
    LAKE_DIR=/lake
WORKDIR /app/analytics
CMD ["python", "run.py", "--dsn", "postgres://postgres:pfaengine@postgres:5432/pfaengine", "--lake", "/lake"]
