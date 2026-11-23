# Build stage
FROM golang:1.24@sha256:d2d2bc1c84f7e60d7d2438a3836ae7d0c847f4888464e7ec9ba3a1339a1ee804 AS builder
WORKDIR /src
COPY ./ ./
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /log-enricher main.go

# Final stage
FROM scratch
COPY --from=builder /log-enricher /log-enricher
ENTRYPOINT ["/log-enricher"]
