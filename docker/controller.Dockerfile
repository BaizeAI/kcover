# builder
FROM m.daocloud.io/docker.io/golang:1.22.2 as builder

WORKDIR /app

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum

RUN go env
RUN go env -w CGO_ENABLED=0
RUN go mod download

ADD . .

RUN go build -ldflags "-s -w" -o fast-recovery-controller ./cmd/fast-recovery

# runner
FROM m.daocloud.io/docker.io/ubuntu:22.04

WORKDIR /app

COPY --from=builder /app/fast-recovery-controller fast-recovery-controller

CMD /app/fast-recovery-controller
