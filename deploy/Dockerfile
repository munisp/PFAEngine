# PFAEngine service image — one Dockerfile builds every cmd/<service>
# binary (Tranche 8 hardening: multi-stage, static, non-root, healthcheck).
#
#   docker build -f deploy/Dockerfile --build-arg SERVICE=member-svc -t pfaengine/member-svc .
#
# Services read PORT (per-service defaults; compose maps host ports 1:1).
FROM golang:1.23 AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /bin/service ./cmd/$SERVICE

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 pfa
COPY --from=build /bin/service /service
USER 10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1
ENTRYPOINT ["/service"]
