# Security policy

## Supported versions

Security fixes are provided on the latest commit of the default branch.
Tagged releases are not maintained as parallel support lines; use a subsequent
release containing the fix when it is published.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability or include secrets,
customer data, prompts, generated media, or exploit details in public channels.

Email vulnerability reports privately to [serge@sociaro.com](mailto:serge@sociaro.com).
You may also use GitHub's **Security → Report a vulnerability** form when it is
enabled for this repository. Do not send sensitive reports through public issues.

Include the affected version or commit, impact, reproduction steps, and any
suggested mitigation. Maintainers will acknowledge a complete report as soon as
practicable, coordinate a fix and disclosure date, and credit the reporter if
requested.

## Deployment security

The included Compose file is for local development. It binds the Gateway to
localhost and uses visible development-only credentials. For any shared or
production deployment:

- replace `CLIENT_API_KEY`, `WEBHOOK_SIGNING_SECRET`,
  `MEDIA_URL_SIGNING_SECRET`, and `PROVIDER_CREDENTIALS_ENCRYPTION_KEY` with
  independent high-entropy values;
- terminate TLS at a trusted reverse proxy and restrict `/v1/admin/*` access;
- use a managed secret store instead of an environment file where possible;
- use least-privilege provider and read-only billing credentials;
- restrict outbound traffic to expected provider, media, and webhook endpoints;
- back up PostgreSQL and media storage, and test recovery;
- keep the base images and Go modules updated.

If a provider or Gateway credential may have been exposed, revoke and rotate it
at its issuer immediately. Removing a secret from the current files does not
remove it from Git history.

## Known preview limitations

Google sessions and personal Gateway keys share owner-scoped tasks and provider
settings. Personal keys have explicit generation, provider-management and
key-management permissions; only hashes of their secrets are stored. Organization
roles, current membership and generation budgets are enforced in PostgreSQL.
Restricted keys do not inherit their owner's system-administrator override.
Email authentication has persistent account, token and network quotas. The
separate legacy service key remains scoped to the system owner.

Revocation blocks new authentication, but does not cancel accepted work or expire
previously signed media URLs. A key with `keys:manage` can revoke account keys and
create independent keys with a subset of its own scopes. Revoking that parent key
does not revoke keys it created. For suspected account compromise, revoke all
affected keys and browser sessions.

Provider, storage and result-download connections check every resolved address
and dial the checked IP directly. Private, loopback, link-local, metadata and
special-use destinations are denied by default. Operator-only
`OUTBOUND_ALLOW_PRIVATE_HOSTS` exceptions use exact hostnames/IPs and also affect
provider/storage traffic; do not allow untrusted destinations. Environment HTTP
proxies are deliberately bypassed. Secret-bearing requests to public destinations
require HTTPS and cannot redirect to another origin. Independent network egress
controls provide additional protection.

Changing a provider API destination requires re-entering retained secrets.
Local media/reference responses are sandboxed, and direct storage URLs must use
a separate hostname from the console/API. Revocation does not expire reference
capability URLs; delete the reference to invalidate access.

Webhook signing
currently uses a deployment-wide secret, not per-account secrets. Coordinate
receiver verification with the operator or use authenticated polling.

The [security testing guide](docs/security-testing.md) describes reproducible
checks and their limits. Verify the exact source and deployed release separately.
