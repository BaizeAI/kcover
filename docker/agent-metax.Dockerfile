ARG MX_SMI_IMAGE=ghcr.io/baizeai/mx-smi:v0.2

FROM --platform=$BUILDPLATFORM m.daocloud.io/docker.io/golang:1.23.2 AS builder

WORKDIR /app

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum

ARG GOPROXY=https://goproxy.cn,direct

RUN go env
RUN go env -w GOPROXY=$GOPROXY
RUN go env -w CGO_ENABLED=0
RUN go mod download

ADD . .

ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -tags metax -ldflags "-s -w" -o kcover-agent ./cmd/agent

FROM ${MX_SMI_IMAGE} AS metax-tools

FROM m.daocloud.io/docker.io/ubuntu:24.04

WORKDIR /app

ENV DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH

ARG DOCA_HOST_REPO_DEB=https://content.mellanox.com/DOCA/DOCA_v3.2.0/host/doca-host_3.2.0-125000-25.10-ubuntu2404_amd64.deb
ARG DOCA_HOST_REPO_DEB_SHA256=5a72f90c39994893b3bcafd07ac3112b5a53076d484ea1051075e6ded70fd666

COPY docker/maca-mxrdma-3.7.2.0-deb-x86_64.tar.xz /tmp/maca-mxrdma-3.7.2.0-deb-x86_64.tar.xz

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		chrony \
		ca-certificates \
		libnl-3-200 \
		libnl-route-3-200 \
		ibverbs-utils \
		wget \
		xz-utils \
		tzdata \
	&& if [ "$TARGETARCH" != "amd64" ]; then \
		echo "kcover-agent-metax supports linux/amd64 only; received TARGETARCH=$TARGETARCH" >&2; \
		exit 1; \
	fi \
	&& wget -O /tmp/doca-host.deb "$DOCA_HOST_REPO_DEB" \
	&& echo "$DOCA_HOST_REPO_DEB_SHA256  /tmp/doca-host.deb" | sha256sum -c - \
	&& dpkg -i /tmp/doca-host.deb \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends doca-ofed-userspace \
	&& mkdir -p /tmp/maca-mxrdma \
	&& tar -xJf /tmp/maca-mxrdma-3.7.2.0-deb-x86_64.tar.xz -C /tmp/maca-mxrdma \
	&& dpkg -i --force-overwrite /tmp/maca-mxrdma/maca-mxrdma-3.7.2.0/mxrdma_*.deb \
	&& apt-get purge -y --auto-remove wget ca-certificates xz-utils \
	&& rm -rf /var/lib/apt/lists/* \
	&& rm -rf /tmp/maca-mxrdma /tmp/doca-host.deb /tmp/maca-mxrdma-3.7.2.0-deb-x86_64.tar.xz

COPY --from=builder /app/kcover-agent kcover-agent
COPY --from=metax-tools /usr/local/bin/mx-smi /usr/local/bin/mx-smi
COPY docker/agent-entrypoint.sh /usr/local/bin/agent-entrypoint.sh

RUN ln -sf "$(command -v ibv_devinfo)" /usr/local/bin/ibv_devinfo \
	&& chmod +x /usr/local/bin/mx-smi /usr/local/bin/ibv_devinfo /usr/local/bin/agent-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/agent-entrypoint.sh"]
