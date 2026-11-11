# Open Media Gateway

**One API. One key. AI media across providers.**

Generate images, video, speech, and music without integrating each provider separately.

[Get started](#get-started) · [Providers](#supported-providers) · [API reference](docs/openai-api.md)

## Why use it?

- **Integrate once.** Submit jobs, track progress, and retrieve results through a consistent API.

- **Keep your outputs.** Store generated media locally or in S3-compatible storage, ready to download and reuse.

- **Understand spending.** See cost estimates before generation and track usage across providers.

- **Run it your way.** Self-host the gateway, connect your provider accounts, and manage team access.

- **One balance for shared providers.** Fund generation from a common company balance, with an invoice for each top-up.

## Supported providers

[OpenAI](docs/openai-provider.md) · [BytePlus ModelArk](docs/configuration.md#provider-bootstrap) · [Alibaba Cloud Model Studio](docs/configuration.md#provider-bootstrap) · [Agnes AI](docs/agnes-provider.md) · [Kling AI](docs/kling-provider.md) · [MiniMax](docs/minimax-provider.md) · [Sonilo](docs/sonilo-provider.md)

## Add a provider

Open Media Gateway is open source. Add an integration and submit a pull request. Our [provider integration guide](docs/provider-development.md) covers the requirements, tests, and submission checklist.

## Get started

1. [Install the gateway](deploy/ghcr/README.md) on your infrastructure.
2. [Connect your providers](docs/provider-connections.md) and choose your models.
3. Create an API key and [make your first request](docs/api-quickstart.md).

## Also included

A browser Playground to try models, plus [templates](docs/templates.md) for reusable prompts.

Licensed under [Apache License 2.0](LICENSE). Bundled third-party components retain their own terms and notices. Provider names are trademarks of their respective owners; this project is independent and is not endorsed by them.
