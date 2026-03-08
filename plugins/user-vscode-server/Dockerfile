# -- Dev stage: hot reload with Air + code-server --
FROM codercom/code-server:latest AS dev

USER root
RUN apt-get update && apt-get install -y --no-install-recommends wget git && rm -rf /var/lib/apt/lists/*
RUN wget -qO- https://go.dev/dl/go1.25.3.linux-$(dpkg --print-architecture).tar.gz | tar -C /usr/local -xzf -
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"
RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/user-vscode-server

COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/user-vscode-server/ /app/plugins/user-vscode-server/

RUN go mod download

EXPOSE 8092 8443
ENTRYPOINT []
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/user-vscode-server/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/user-vscode-server/ plugins/user-vscode-server/
WORKDIR /app/plugins/user-vscode-server
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /user-vscode-server .

# -- Prod stage: code-server base + Go binary --
FROM codercom/code-server:latest AS prod
USER root
COPY --from=builder /user-vscode-server /usr/local/bin/user-vscode-server
RUN mkdir -p /workspaces && chown coder:coder /workspaces
USER coder
EXPOSE 8092
ENTRYPOINT []
CMD ["user-vscode-server"]
