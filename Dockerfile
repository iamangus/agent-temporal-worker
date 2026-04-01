FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /worker ./cmd/worker/

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /worker /usr/local/bin/worker
COPY definitions/ /data/definitions/

WORKDIR /data

# TEMPORAL_HOST_PORT — address of the Temporal frontend (default: localhost:7233)
ENV TEMPORAL_HOST_PORT=localhost:7233

ENTRYPOINT ["worker"]
