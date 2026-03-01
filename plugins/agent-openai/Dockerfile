FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy SDK first
COPY pkg/pluginsdk/ pkg/pluginsdk/

# Copy plugin
COPY plugins/agent-openai/ plugins/agent-openai/

WORKDIR /app/plugins/agent-openai
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-openai .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /agent-openai /usr/local/bin/agent-openai
RUN mkdir -p /data && chown appuser:appuser /data

USER appuser
EXPOSE 8081
CMD ["agent-openai"]
