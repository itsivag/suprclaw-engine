# ============================================================
# Stage 1: Build the suprclaw binary
# ============================================================
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN make build

# ============================================================
# Stage 2: Minimal runtime image
# ============================================================
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata curl

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:18790/health || exit 1

# Copy binary
COPY --from=builder /src/build/suprclaw /usr/local/bin/suprclaw

# Create non-root user and group
RUN addgroup -g 1000 suprclaw && \
    adduser -D -u 1000 -G suprclaw suprclaw

# Switch to non-root user
USER suprclaw

# Run onboard to create initial directories and config
RUN /usr/local/bin/suprclaw onboard

ENTRYPOINT ["suprclaw"]
CMD ["gateway"]
