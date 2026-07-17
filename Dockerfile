# Build
FROM golang:1.24-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/smart-alert-aggregator ./cmd

# Runtime — run as root so bind-mounted ./data is always writable
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /out/smart-alert-aggregator /app/smart-alert-aggregator
COPY config/config.example.yaml /app/config/config.yaml
RUN mkdir -p /app/data

USER root
EXPOSE 8088

ENTRYPOINT ["/app/smart-alert-aggregator"]
CMD ["-config", "/app/config/config.yaml"]
