ARG GO_BUILDER_IMAGE=m.daocloud.io/docker.io/library/golang:1.27.1-alpine
FROM --platform=$BUILDPLATFORM ${GO_BUILDER_IMAGE} AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum

ARG GOPROXY=https://goproxy.cn,direct

RUN go env
RUN go env -w GOPROXY=$GOPROXY
RUN go env -w CGO_ENABLED=0
RUN go mod download

ADD . .

ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-s -w" -o kcover-agent ./cmd/agent

FROM m.daocloud.io/docker.io/ubuntu:24.04

WORKDIR /app

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		ca-certificates \
		tzdata \
	&& rm -rf /var/lib/apt/lists/* \
	&& rm -rf /tmp/*

COPY --from=builder /app/kcover-agent kcover-agent
COPY docker/agent-entrypoint.sh /usr/local/bin/agent-entrypoint.sh

RUN chmod +x /usr/local/bin/agent-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/agent-entrypoint.sh"]
