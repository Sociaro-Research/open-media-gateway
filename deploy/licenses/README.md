Runtime Go module notices are collected from checksum-verified module archives.
The application image includes them under `/licenses`, with `modules.json`.
The Go toolchain notice is included there too.

The BytePlus SDK v1.0.16 archive omits its root license. Its upstream repository
declares Apache-2.0. The accompanying fallback is copied verbatim from its
upstream LICENSE at commit `349d7cac2ef5d11cef17b07ffbbabfd2d4c39c55`:
https://github.com/byteplus-sdk/byteplus-sdk-golang/blob/349d7cac2ef5d11cef17b07ffbbabfd2d4c39c55/LICENSE

Review this fallback when upgrading that dependency; unknown missing notices
fail the image build rather than silently disappearing.

The independent UI image includes the project's `/LICENSE` and `/NOTICE` from
its own build context. Release preflight checks those copies against the root
notices. The standalone UI archive includes the same root notices. The vendored
`js-sha256` implementation keeps its MIT notice in
`web/public/ui/sha256-LICENSE.txt`, which is shipped with the UI assets.

The UI image separately collects the exact rebuilt Caddy runtime Go graph under
`/licenses`. Its `modules.json` records module versions and SHA-256 hashes of
unmodified license/copying/notice files; unknown missing notices stop the build.
It also includes the Go toolchain license. The collector uses the existing build
module and does not change dependency versions. Its isolated tests run during
the UI build and can be reproduced from the source root with:

```sh
go test web/collect-licenses.go web/collect-licenses_test.go
```

Local source images use `unversioned-source` UI metadata; publication tooling
provides the actual release version/revision labels. These notices do not replace
review of third-party artwork rights or inherited operating-system terms.

Override Sound invokes the Alpine FFmpeg executable as a separate process. The
application runtime now includes the Alpine FFmpeg and FFmpeg documentation
packages, their shared libraries and package notices, and records exact installed
package versions in `/licenses/alpine-packages.txt`. Distribution source recipes
and source archives are available via https://gitlab.alpinelinux.org/alpine/aports
and https://distfiles.alpinelinux.org/. FFmpeg licensing/build configuration is
reported by `ffmpeg -L` and `ffmpeg -buildconf`; review these alongside the image
SBOM before redistributing a release. The gateway's video path uses stream copy,
not FFmpeg video encoders.
