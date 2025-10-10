# Build stage
FROM golang:1.24 AS builder
WORKDIR /src
COPY ./ ./
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /log-enricher main.go

# Final stage
FROM scratch
COPY --from=builder /log-enricher /log-enricher
ENTRYPOINT ["/log-enricher"]
