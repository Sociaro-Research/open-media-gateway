FROM golang:1.27.3-alpine AS caddy-build
WORKDIR /src
# Keep the current Caddy modules/configuration; rebuild with patched Go and HTTP/2.
RUN go mod init gateway-caddy-build && \
    go get github.com/caddyserver/caddy/v2/cmd/caddy@v2.11.7 golang.org/x/net@v0.60.0 && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/caddyserver/caddy/v2.CustomVersion=v2.11.7" -o /out/caddy github.com/caddyserver/caddy/v2/cmd/caddy

COPY collect-licenses.go collect-licenses_test.go /tmp/
RUN go test /tmp/collect-licenses.go /tmp/collect-licenses_test.go && \
    go run /tmp/collect-licenses.go /out/licenses

FROM caddy:2.11.7-alpine
# Require the patched zlib revision without upgrading unrelated OS packages.
RUN apk add --no-cache 'zlib>=1.3.2-r1'
# This fresh binary has no bind capability; the internal UI listens on 8080.
COPY --from=caddy-build /out/caddy /usr/bin/caddy
COPY --from=caddy-build /out/licenses /licenses
LABEL org.opencontainers.image.title="Media Gateway UI" \
      org.opencontainers.image.description="Independent Media Gateway browser console, documentation and API proxy" \
      org.opencontainers.image.source="https://github.com/Sociaro-Research/open-media-gateway" \
      org.opencontainers.image.url="https://github.com/Sociaro-Research/open-media-gateway" \
      org.opencontainers.image.documentation="https://github.com/Sociaro-Research/open-media-gateway#quick-start" \
      org.opencontainers.image.vendor="Sociaro contributors" \
      org.opencontainers.image.version="unversioned-source" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN addgroup -S -g 10001 gateway-ui && adduser -S -u 10001 -G gateway-ui gateway-ui && \
    chown -R gateway-ui:gateway-ui /config /data
COPY Caddyfile /etc/caddy/Caddyfile
COPY public /srv
COPY LICENSE /LICENSE
COPY NOTICE /NOTICE
USER 10001:10001
