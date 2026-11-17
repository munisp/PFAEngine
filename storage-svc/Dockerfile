# syntax=docker/dockerfile:1
# storage-svc — OpenDAL object-storage microservice (see docs/OPENDAL.md).
#
# Multi-stage: build with the official Rust toolchain, ship a minimal
# non-root runtime image. The default build enables the s3 backend;
# override with --build-arg FEATURES="s3,gcs,azblob" (or "" for
# fs/memory-only).

FROM rust:1.83-bookworm AS build
WORKDIR /app

# Cache-friendly dependency layer.
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs \
    && cargo build --release --locked || true \
    && rm -rf src

COPY src ./src
ARG FEATURES=s3
RUN touch src/main.rs \
    && if [ -n "$FEATURES" ]; then \
         cargo build --release --locked --features "$FEATURES"; \
       else \
         cargo build --release --locked; \
       fi \
    && strip target/release/storage-svc

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 --home /nonexistent storage \
    && mkdir -p /var/lib/storage-svc && chown storage /var/lib/storage-svc
COPY --from=build /app/target/release/storage-svc /usr/local/bin/storage-svc
USER storage
EXPOSE 8110
ENV LISTEN_ADDR=0.0.0.0:8110 \
    STORAGE_BACKEND=fs \
    FS_ROOT=/var/lib/storage-svc
ENTRYPOINT ["/usr/local/bin/storage-svc"]
