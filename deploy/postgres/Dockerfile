# Keep the official PostgreSQL startup behavior, replacing only its old Go helper.
FROM golang:1.27.3-alpine AS gosu-build
WORKDIR /src
RUN go mod init gateway-gosu-build && \
    go get github.com/tianon/gosu@v0.0.0-20250923190938-6456aaa0f3c8 golang.org/x/sys@v0.47.0 && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gosu github.com/tianon/gosu

FROM postgres:17-alpine
RUN apk upgrade --no-cache
COPY --from=gosu-build /out/gosu /usr/local/bin/gosu
