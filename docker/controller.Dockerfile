# builder
FROM --platform=$BUILDPLATFORM m.daocloud.io/docker.io/golang:1.22.2 as builder

WORKDIR /app

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum

RUN go env
RUN go env -w CGO_ENABLED=0
RUN go mod download

ADD . .

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-s -w" -o kcover-controller ./cmd/kcover

# runner
FROM m.daocloud.io/docker.io/ubuntu:22.04

WORKDIR /app

COPY --from=builder /app/kcover-controller kcover-controller

CMD /app/kcover-controller
