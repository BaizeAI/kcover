# builder
ARG GO_BUILDER_IMAGE=m.daocloud.io/docker.io/library/golang:1.25.5-alpine
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
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-s -w" -o kcover-controller ./cmd/kcover

# runner
FROM m.daocloud.io/docker.io/ubuntu:22.04

WORKDIR /app

COPY --from=builder /app/kcover-controller kcover-controller

CMD ["/app/kcover-controller"]
