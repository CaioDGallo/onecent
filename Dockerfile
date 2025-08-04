FROM rust:1.85-alpine AS chef
RUN apk add --no-cache git tzdata musl-dev
RUN cargo install --locked cargo-chef
WORKDIR /build

FROM chef AS planner
COPY . .
RUN cargo chef prepare --recipe-path recipe.json

FROM chef AS builder
COPY --from=planner /build/recipe.json recipe.json
RUN cargo chef cook --release --target x86_64-unknown-linux-musl --recipe-path recipe.json

COPY . .
RUN cargo build --release --target x86_64-unknown-linux-musl && \
    strip target/x86_64-unknown-linux-musl/release/onecent

RUN apk add --no-cache upx && \
    upx --best --lzma target/x86_64-unknown-linux-musl/release/onecent

FROM alpine:3.19

RUN apk add --no-cache tzdata

COPY --from=builder /etc/passwd /etc/passwd

COPY --from=builder /build/target/x86_64-unknown-linux-musl/release/onecent /app

RUN addgroup -S appuser && adduser -S appuser -G appuser; \
    mkdir -p /data && chown appuser:appuser /data

USER appuser

EXPOSE 8080

ENTRYPOINT ["/app"]