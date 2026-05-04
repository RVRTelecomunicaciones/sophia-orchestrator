# syntax=docker/dockerfile:1.7
# Multi-stage build for sophia-orchestator. Producción ships only the
# orchestrator binary, migrations directory, and CA roots — no Go toolchain,
# no shell, no busybox. Total image ~25-30MB.

ARG GO_VERSION=1.26.2

# --- builder ---------------------------------------------------------------
FROM golang:${GO_VERSION}-alpine AS builder

# Build tools needed only inside the builder.
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache go.sum/go.mod separately to maximise Docker layer reuse.
COPY go.mod go.sum ./
RUN go mod download

# Source.
COPY cmd/      cmd/
COPY internal/ internal/
COPY migrations/ migrations/

# Static, stripped binary. CGO disabled for distroless final stage.
ARG VERSION=dev
ARG COMMIT=unknown
ENV CGO_ENABLED=0 GOOS=linux

RUN go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/sophia-orchestator \
    ./cmd/sophia-orchestator


# --- runner ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runner

# Copy the orchestrator binary, migrations, and TZ database (needed by some
# pgx datetime conversions).
COPY --from=builder /out/sophia-orchestator /usr/local/bin/sophia-orchestator
COPY --from=builder /src/migrations         /var/sophia/migrations
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo     /usr/share/zoneinfo

ENV SOPHIA_DB_MIGRATIONS_PATH=/var/sophia/migrations/postgres \
    TZ=UTC

USER nonroot:nonroot

EXPOSE 8080

# Distroless static has no shell, so we cannot run a HEALTHCHECK script
# inline. Compose / k8s should poll /api/v1/health from outside the container.

ENTRYPOINT ["/usr/local/bin/sophia-orchestator"]
