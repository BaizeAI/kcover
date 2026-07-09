# builder
FROM --platform=$BUILDPLATFORM m.daocloud.io/docker.io/golang:1.26.5 AS builder

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
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-s -w" -o kcover-controller ./cmd/kcover

# runner
FROM m.daocloud.io/docker.io/ubuntu:26.04

WORKDIR /app

COPY --from=builder /app/kcover-controller kcover-controller

CMD ["/app/kcover-controller"]
