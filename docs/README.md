# Documentation

Start with [Quick Start](../README.md#quick-start) for a local release installation.
Use the README bundled with your chosen release; installation files and images
must come from the same version. For HTTPS, other installation modes, updates,
and troubleshooting, see the [installation guide](../deploy/ghcr/README.md).

## Use and operate Gateway

- [Windows installation with Docker Desktop and WSL 2](windows-wsl.md)
- [Configuration](configuration.md), [release installation](../deploy/ghcr/README.md),
  [deployment and rollback](deployment.md), [GitHub Actions deployment](github-actions-deployment.md)
- [First owner and administration](system-administration.md), [email authentication](email-authentication.md)
- [Organizations and membership](organizations.md), [shared resources](shared-resources.md),
  [payments](payments.md), [audit history](audit-log.md)
- [Provider connections](provider-connections.md), [provider asset libraries](provider-asset-library.md),
  [model catalogue](model-catalog.md), [catalogue synchronization](catalog-sync.md)
- Providers: [OpenAI](openai-provider.md), [Agnes](agnes-provider.md), [Sonilo](sonilo-provider.md);
  BytePlus and Alibaba setup is covered in [configuration](configuration.md)
- [Reference library](reference-library.md), [direct S3 transfers](direct-storage.md),
  [branding](site-branding.md), [public documentation sites](public-sites.md), [monitoring](monitoring.md)

## Integrate with the API

- [First API generation: create, poll, and download](api-quickstart.md)
- [Asynchronous media API](openai-api.md) and [OpenAPI specification](openapi.yaml)
- [Request limits](request-limits.md), [cost accounting](cost-accounting.md), [webhooks](webhooks.md)
- Your running installation serves its reference at `/docs` and its specification
  at `/openapi.yaml` and `/openapi.json`.

## Develop and verify

- [Source setup and development](development.md), [contributing](../CONTRIBUTING.md), [architecture](architecture.md),
  [frontend/headless operation](frontend-separation.md), [concurrent workers](concurrent-workers.md)
- [Provider module contract](provider-modules.md), [provider development](provider-development.md),
  [UI design system](design-system.md)
- [Testing](testing.md), [browser fixtures](../scripts/ui/README.md),
  [reliability/performance](performance-testing.md), [security checks](security-testing.md)
- [Security policy](../SECURITY.md), [public release checklist](open-source-release-checklist.md)
- [Maintainer release guide](ghcr-release.md)

Store run logs, screenshots, review notes and dated investigation records in
ignored `dist/` or `.local/`, or CI artifacts. Public documentation describes
supported behavior and how to reproduce checks.
