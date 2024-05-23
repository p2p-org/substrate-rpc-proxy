FROM golang:1.22-alpine as builder

WORKDIR /build/

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN \
  go build -v -o consumer ./cmd/consumer/ && \
  go build -v -o proxy ./cmd/proxy/

FROM alpine:3.19
COPY --from=builder /build/consumer /build/proxy /app/

ENTRYPOINT []