FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/muse2api ./cmd/muse2api

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 muse2api && useradd --uid 1000 --gid 1000 --no-create-home muse2api \
    && mkdir /app && chown muse2api:muse2api /app
WORKDIR /app
COPY --from=build /out/muse2api /usr/local/bin/muse2api
COPY LICENSE NOTICE.md /usr/share/doc/muse2api/
COPY LICENSES /usr/share/doc/muse2api/LICENSES
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/muse2api"]
CMD ["serve"]
