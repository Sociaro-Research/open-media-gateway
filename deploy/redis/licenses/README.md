# Redis 7.4.11 third-party notices

Redis is a separate third-party runtime component. The Gateway's own code
remains licensed under Apache License 2.0. The files here preserve Redis's
upstream license alternatives and copyright notices; they do not select an
alternative or relicense Redis under the Gateway's license.

- `LICENSE.txt`: the complete upstream Redis license text, unchanged.
- `REDISCONTRIBUTIONS.txt`: the upstream copyright and BSD-3-Clause contribution
  notice, unchanged.
- `deps/`: complete upstream notices for fpconv, hdr_histogram, hiredis,
  jemalloc and Lua, unchanged.
- `SOURCE-NOTICES.txt`: verbatim copyright/license comment blocks from upstream
  C-family files under `src/` and `deps/`, including inline notices such as
  linenoise, CRC, compression and hashing code. Identical blocks appear once;
  delimiters identify their source locations. Optional/test source notices may
  also be retained; their inclusion does not assert that every source file is
  compiled into every Redis binary.
- `provenance.json`: original source paths, byte offsets and SHA-256 hashes for
  the copied files and individual comment blocks.

The source is the upstream Redis 7.4.11 archive:
https://github.com/redis/redis/archive/refs/tags/7.4.11.tar.gz

Its verified SHA-256 is
`e973da69febfea096ab94690b44bf976482788a1b5e070df373b4f27697d57d4`.
The official container recipe recorded in `provenance.json` uses this same
source checksum. No Redis source code was changed to prepare these notices.

The release Redis image copies this directory to `/licenses/redis/`; the
installation bundle copies the same explicit file set to `licenses/redis/`.
The image build requires Redis 7.4.11 so a moving upstream image tag cannot
silently receive notices for a different Redis version. Review the complete
notice set and source provenance together when intentionally changing versions.
