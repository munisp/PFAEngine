#!/bin/sh
# verify-dockerfile.sh — static checks for deploy/Dockerfile hardening
# (Tranche 8). We cannot run `docker build` in every environment, so this
# script asserts the properties that matter: multi-stage build, static
# CGO-free binary, non-root runtime user, exposed port, healthcheck, and a
# SERVICE build arg so one Dockerfile builds all services.
set -u

DF="${1:-deploy/Dockerfile}"
fails=0

check() { # check <description> <grep-pattern>
    if grep -Eq "$2" "$DF" 2>/dev/null; then
        echo "ok    - $1"
    else
        echo "FAIL  - $1"
        fails=$((fails + 1))
    fi
}

if [ ! -f "$DF" ]; then
    echo "FAIL  - $DF not found"
    exit 1
fi

stages=$(grep -c '^FROM ' "$DF")
if [ "$stages" -ge 2 ]; then
    echo "ok    - multi-stage build ($stages stages)"
else
    echo "FAIL  - expected multi-stage build, found $stages FROM line(s)"
    fails=$((fails + 1))
fi

check "build arg for service binary name" '^ARG SERVICE'
check "module files copied before sources (layer caching)" '^COPY go\.mod go\.sum'
check "static CGO-free build" 'CGO_ENABLED=0'
check "non-root runtime user" '^USER (10001|pfa)'
check "no root user in runtime stage" '^USER (10001|pfa)$'
check "port exposed" '^EXPOSE 8080'
check "healthcheck present" '^HEALTHCHECK'
check "healthcheck hits /healthz" 'HEALTHCHECK.*healthz|/healthz'
check "exec-form entrypoint" '^ENTRYPOINT \["/service"\]'

if grep -Eq '^USER (root|0)$' "$DF"; then
    echo "FAIL  - runtime must not run as root"
    fails=$((fails + 1))
fi

if [ "$fails" -gt 0 ]; then
    echo "verify-dockerfile: FAIL ($fails check(s) failed)"
    exit 1
fi
echo "verify-dockerfile: PASS"
