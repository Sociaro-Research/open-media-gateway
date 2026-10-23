# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates python3
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/webhook-receiver ./cmd/webhook-receiver
RUN python3 deploy/ghcr/collect-licenses.py /out/licenses
RUN mkdir -p /out/media && chown 65532:65532 /out/media
RUN mkdir -p /out/rootfs/tmp && chmod 1777 /out/rootfs/tmp

FROM scratch
LABEL org.opencontainers.image.title="Media Gateway" \
      org.opencontainers.image.description="Provider-neutral asynchronous API for image, video, speech, and music generation" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gateway /gateway
COPY --from=build /out/worker /worker
COPY --from=build /out/webhook-receiver /webhook-receiver
COPY --from=build --chown=65532:65532 /out/media /data/media
# Copy the parent so BuildKit preserves the child directory's sticky mode.
COPY --from=build --chown=65532:65532 /out/rootfs/ /
COPY LICENSE /LICENSE
COPY NOTICE /NOTICE
COPY --from=build /out/licenses /licenses
USER 65532:65532
ENTRYPOINT ["/gateway"]
