# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache gcc musl-dev sqlite
RUN go install github.com/air-verse/air@latest

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o kernel .

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /app/kernel .

RUN mkdir -p /data

EXPOSE 8080

ENTRYPOINT ["./kernel"]
