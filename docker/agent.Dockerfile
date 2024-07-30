# builder
FROM m.daocloud.io/docker.io/golang:1.22.2 as builder

WORKDIR /app

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum

RUN go env
RUN go env -w CGO_ENABLED=0
RUN go mod download

ADD . .

RUN go build -ldflags "-s -w" -o fast-recovery-agent ./cmd/collector-controller

# runner
FROM m.daocloud.io/docker.io/ubuntu:22.04

WORKDIR /app

# todo install dcgm toolkit?

COPY --from=builder /app/fast-recovery-agent fast-recovery-agent

CMD /app/fast-recovery-agent
