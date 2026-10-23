# Open Media Gateway

**Generate AI images, video, speech, and music through one self-hosted workspace and API.**

Open Media Gateway connects your media provider accounts to a shared browser
console and an asynchronous API. It is for developers building media features
into applications and teams that want one place to generate, store, and manage
media across providers.

## Why use it?

- **Integrate once.** Submit jobs through a common API, check their status, and
  receive signed webhooks. Gateway handles provider requests, polling, and result
  retrieval behind the scenes.
- **Give your team a workspace.** Try models in the Playground, reuse reference
  files, and share providers and generation history within organizations.
- **Keep results in your storage.** Archive generated files locally or in
  S3-compatible storage, with authenticated downloads and signed preview URLs.
- **See usage and costs.** Review generation history, provider usage, and cost
  estimates where pricing is available. Estimates are not guaranteed final charges.
- **Start without paid credentials.** The included `mock` provider returns fixed
  test media so you can try the console and API before connecting a paid account.

Included adapters: [OpenAI](docs/openai-provider.md),
[BytePlus ModelArk and Alibaba Cloud Model Studio](docs/configuration.md#provider-bootstrap),
[Agnes AI](docs/agnes-provider.md), and [Sonilo](docs/sonilo-provider.md), plus `mock`.
Media types and controls depend on the selected provider and model. Real generation
uses your provider credentials and may incur charges. The API uses OpenAI-style
endpoints with an [asynchronous job lifecycle](docs/openai-api.md); clients must
poll or handle webhooks before downloading results.

## Quick Start

Run a new local installation with the published **v1.0.1** bundle. It contains
configuration for Linux AMD64/ARM64 images; no source checkout, Go, Node, or image
build is needed.

**Requirements:** Docker Engine or Docker Desktop running, **Compose 2.24.4+**,
**Python 3.9+**, and a Linux/macOS/WSL shell. On Windows, use Docker Desktop with
[Ubuntu in WSL 2](docs/windows-wsl.md) and run the
commands in Ubuntu. No provider account or paid key is needed for `mock`.

### 1. Download and verify

Download the [installation archive](https://github.com/Sociaro-Research/open-media-gateway/releases/download/v1.0.1/media-gateway-v1.0.1.tar.gz)
and [SHA-256 file](https://github.com/Sociaro-Research/open-media-gateway/releases/download/v1.0.1/media-gateway-v1.0.1.tar.gz.sha256)
into a new, empty directory. Then run there:

```sh
sha256sum -c media-gateway-v1.0.1.tar.gz.sha256
tar -xzf media-gateway-v1.0.1.tar.gz
cd media-gateway-v1.0.1
```

On macOS, use `shasum -a 256 -c` instead of `sha256sum -c`. Stop if verification
fails. For other versions, use matching assets from
[GitHub Releases](https://github.com/Sociaro-Research/open-media-gateway/releases)
and follow that bundle's README.

### 2. Start and create your owner account

```sh
python3 initialize.py
docker compose pull
docker compose up -d --wait
docker compose exec -T gateway /gateway admin setup-code
```

Open [http://localhost:8088](http://localhost:8088), enter the private setup code in
**Create owner account**, and choose your name, email, and password. You are
signed in as Administrator; email delivery and Google sign-in are optional.

### 3. Try a free generation

In **Playground**, select **Mock Image**, enter a prompt, and generate. The mock
returns a fixed SVG, so it will not render your prompt or call a paid service.
For an application integration, follow the [create, poll, and download example](docs/api-quickstart.md).

Keep `.env` and Docker volumes: they contain credentials and data. Do not
reinitialize an existing installation, change its encryption keys, or run
`docker compose down -v` to restart it. Local ports bind to loopback; before
public use, follow [server installation and updates](deploy/ghcr/README.md) and
the [deployment guide](docs/deployment.md). Review and replace the included
privacy and terms operator templates for your installation. Keep development configurations private.

## Learn more

- [Installation options, HTTPS, troubleshooting, and updates](deploy/ghcr/README.md)
- [Provider, storage, and account configuration](docs/configuration.md)
- [API integration and webhooks](docs/openai-api.md)
- [Build from source and run checks](docs/development.md) · [Contribute](CONTRIBUTING.md)
- [All documentation](docs/README.md) · [Security](SECURITY.md) · [Support](SUPPORT.md)

Licensed under [Apache License 2.0](LICENSE). Bundled third-party components
retain their own terms and notices. Provider names are trademarks of their
respective owners; this project is independent and is not endorsed by them.
