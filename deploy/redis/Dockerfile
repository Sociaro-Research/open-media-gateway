# Apply published Alpine security patches without changing the Redis major line.
FROM redis:7.4.12-alpine3.22@sha256:acc6f0ac39f6fe3a39c57b3c943832f24f4eeff4e17f24960668e37d1e112a36
# Keep the retained notices bound to the Redis version reviewed for this release.
RUN test "$REDIS_VERSION" = "7.4.12" && apk upgrade --no-cache
COPY licenses/README.md licenses/LICENSE.txt licenses/REDISCONTRIBUTIONS.txt licenses/SOURCE-NOTICES.txt licenses/provenance.json /licenses/redis/
COPY licenses/deps/fpconv/LICENSE.txt /licenses/redis/deps/fpconv/
COPY licenses/deps/hdr_histogram/COPYING.txt licenses/deps/hdr_histogram/LICENSE.txt /licenses/redis/deps/hdr_histogram/
COPY licenses/deps/hiredis/COPYING /licenses/redis/deps/hiredis/
COPY licenses/deps/jemalloc/COPYING /licenses/redis/deps/jemalloc/
COPY licenses/deps/lua/COPYRIGHT /licenses/redis/deps/lua/
