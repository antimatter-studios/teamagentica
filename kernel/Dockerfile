# Stage 1: Build
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o kernel .

# Stage 2: Runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

WORKDIR /app
COPY --from=builder /app/kernel .

RUN mkdir -p /data && chown appuser:appuser /data

USER appuser
EXPOSE 8080

ENV ROBOSLOP_DB_PATH=/data/roboslop.db

ENTRYPOINT ["./kernel"]
