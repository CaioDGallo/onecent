FROM golang:1.24.4-alpine AS builder

RUN apk add --no-cache git tzdata

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags="-s -w" \
  -trimpath \
  #TODO: Remove before submit
  -tags dev \
  -o app \
  ./cmd/api/main.go

RUN apk add --no-cache upx && \
  upx --best --lzma app

FROM alpine:3.19

RUN apk add --no-cache tzdata

COPY --from=builder /etc/passwd /etc/passwd

COPY --from=builder /build/app /app

RUN addgroup -S appuser && adduser -S appuser -G appuser; \
  mkdir -p /data && chown appuser:appuser /data

USER appuser

EXPOSE 8080

ENV GOGC=75 \
  GOMEMLIMIT=25MiB \
  GOMAXPROCS=1

ENTRYPOINT ["/app"]
