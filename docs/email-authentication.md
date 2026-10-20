# Email authentication

The login card offers the sign-in methods enabled in **Administration → Settings → Login methods**. Existing password accounts can sign in without email delivery. Registration and sending recovery links require a configured delivery provider. Email supports registration, confirmation,
password login, resending confirmation and password recovery. All verified users
use the same HttpOnly browser sessions, organization memberships and API-key rules.
Creating an email account does not join an organization or grant administrator rights.

When signup starts from an organization invitation, `register` includes its
`invitation_token`. The server encrypts this continuation in the email challenge
using a domain-separated key derived from the stable `SESSION_SIGNING_SECRET`.
Verification transfers it to the restricted registration grant. The confirmed
browser restores it before password setup and after a grant resume, so opening
the email in another tab/browser or closing the original tab retains the intended
organization. `forgot`/`reset` preserve the same continuation for existing accounts.
No raw invitation is stored in the database or added to the outgoing email link.
Only responses proving mailbox ownership return it; generic mail-request responses
and requests without the grant expose no continuation. Keep the session signing
secret stable while links/grants are outstanding.

After sign-in, the console shows the existing **Accept invitation** action.
Registration never accepts an invitation or grants organization access. The normal
preview and accept endpoints recheck the invitation's current validity, recipient,
expiry and revocation; unavailable invitations do not prevent ordinary account
creation. Migration 067 adds nullable encrypted context, preserving old links and
registrations without an invitation.

The installation's first owner uses a separate one-time
[setup-code workflow](system-administration.md#first-administrator), which needs
no email provider and grants the initial Administrator role.

## Delivery configuration

In **Administration → Settings → Email delivery**, select **Resend**, enter
an API key with sending access and a sender address (for example,
`Sociaro <login@example.com>`), then save. The sender's domain must be verified in
Resend. The console offers only supported mail providers; currently that is Resend.
The connection is separate from generation providers and is editable only by a
system administrator, not an organization administrator.

Settings take effect on all gateway replicas without restarting. The login card
shows **Continue with email** when the Email switch is enabled.
Selecting **Not configured** removes the delivery connection; it does not change
login-method switches. Without delivery, requesting registration/recovery emails
is blocked; existing password login, verified grants, links and sessions remain
usable while the Email switch is enabled.
Saving performs local format validation, not a provider connection test. Invalid or
revoked keys and an unverified sending domain will produce a delivery error when
someone requests an email; saving settings never sends real mail.

The Resend key is encrypted in PostgreSQL using the installation's existing
`PROVIDER_CREDENTIALS_ENCRYPTION_KEY`. Neither the key nor its ciphertext is returned
by the API or copied into the activity log. A blank key field preserves the existing
key; changing it replaces the encrypted value. Version checks reject stale edits.
Keep the encryption secret stable across restarts and replicas.

For existing installations, `RESEND_API_KEY` and `EMAIL_FROM` in the server
environment seed an untouched configuration once. Later console edits, including
removing the connection, take precedence and are not overwritten on restart.
For email-only deployments, also set `SESSION_SIGNING_SECRET` (at least 32
characters) and `AUTH_PUBLIC_URL` to the site's HTTPS origin. With Google already
configured, the origin defaults to `GOOGLE_OAUTH_REDIRECT_URL`'s origin. Only
localhost may use HTTP. Link URLs never derive from request Host or forwarding headers.

The sender uses the [Resend email endpoint](https://resend.com/docs/api-reference/emails/send-email)
with a timeout and up to three attempts for transient/network failures, retaining
one [idempotency key](https://resend.com/docs/dashboard/emails/idempotency-keys).
Delivery errors do not include tokens, response bodies or credentials. After a
failed delivery the user can retry; the new request replaces the previous link.
There is no durable outbound mail queue in this version.

## Identity and verification

Addresses are trimmed and compared case-insensitively across all methods. Dots
and plus-tags are preserved. A unique index on `LOWER(users.email)` prevents
ambiguous ownership. Migration 026 fails rather than silently merging any legacy
users with duplicate addresses; check duplicates before deployment.

Registration starts with an email address and optional invitation context. It never accepts or stores a
password before email ownership has been verified. The email link expires after
one hour. Opening it automatically exchanges the single-use token for a separate
30-minute registration grant in a Secure, HttpOnly, SameSite=Strict cookie scoped
to `/auth/email`. The grant can only show the confirmed address and set the initial
password; it cannot authenticate requests to the service. No user, credential or
normal session exists yet.

The confirmed owner chooses a password once. Saving it atomically consumes the
grant, creates the user and Argon2id credential, invalidates pending challenges,
and creates a normal browser session. A failed session insert rolls everything
back so the same grant can be retried. Refreshing the page resumes password setup
while the grant remains valid. Completion, login and password reset clear the
registration cookie. Expired grants require another confirmation email.

Migration 030 clears passwords from legacy pending registrations. Unused old email
links enter this same email-first flow; they cannot activate a password chosen by
someone else. Existing verified accounts are preserved. Requesting a new email
invalidates the previous email link but does not cancel an already verified grant;
redeeming a newer link replaces that grant. Accounts created concurrently through
another login method cannot be overwritten or silently linked.

Registering an existing account never replaces its password or owner ID. Existing
Google users can choose **Forgot password?**, prove mailbox ownership and set an
email password for the same account. Google sign-in remains available to them.
A new Google subject is never automatically linked to an email-only account just
because its address matches; use email sign-in for that account. Linking such a
Google identity would require a separate authenticated account-linking flow.

## Passwords, tokens and sessions

Following [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html),
passwords use Argon2id (19 MiB, two iterations, one lane), a random 16-byte salt and
a 32-byte hash. Passwords accept 15–128 Unicode characters, up to 512 bytes, with
no trimming or composition rules. Password managers and paste are supported.
At most four password operations execute concurrently in each gateway process.

Each email authentication form has a separate purpose with stable input IDs and a
POST action. Sign-in uses `autocomplete="current-password"`;
password setup/reset use `autocomplete="new-password"` and password-generation length
rules. Email inputs use `autocomplete="username"`. The shared password control
provides an accessible show/hide button and preserves the entered value and caret.
API credentials keep their separate non-password behavior. Native autofill and
password-generation offers depend on the user's browser/password-manager settings.
See [Chromium's password form guidance](https://www.chromium.org/developers/design-documents/form-styles-that-chromium-understands/).

Confirmation and reset tokens use 32 cryptographically random bytes; only SHA-256
hashes are stored. Links carry tokens in URL fragments; the client removes them
from the URL and submits them in JSON. Raw tokens never enter query strings, logs,
localStorage or sessionStorage. Registration grants are returned only as HttpOnly cookies. GET requests do not consume links. Opening a confirmation link in a browser that
executes the page JavaScript submits the token automatically; simple HTTP link
scanners do not consume it. Reset still requires the user to choose a new password.

Reset links expire after 30 minutes and are single-use. Successful reset replaces
the password, invalidates other pending links and revokes all browser sessions
atomically. It does not revoke API keys. Login rechecks and locks the credential
when inserting a session, preventing a racing old-password login after reset.
Reset does not automatically sign in. See [OWASP recovery guidance](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html).

JSON-only requests are wrapped in the server's cross-origin protection. Sessions
use Secure (on HTTPS), HttpOnly and SameSite=Lax cookies, with the configured
`SESSION_TTL`. Registration and recovery use generic responses for existing and
missing accounts; login uses the same message for unknown, unverified and incorrect
credentials, with a dummy Argon2 verification for unknown accounts.

PostgreSQL rate limits survive restarts and are shared by replicas: 10 attempts
per address/action or token per 15 minutes, 3 mail requests per address per hour,
and 300 requests per network peer per 15 minutes. Forwarding headers are not
trusted; behind a reverse proxy, the network quota is aggregate for that proxy.
Expired limits and challenges are cleaned during subsequent requests.

## Verification without sending email

`internal/postgres/email_auth_test.go` runs the full lifecycle against an isolated
PostgreSQL database with an in-memory mail sink. It covers single-use concurrency,
the email-first boundary, legacy links, grant expiry, password reset/session invalidation, Google-account recovery, lack of
implicit identity linking, throttling and disabled delivery. Resend tests use a
fake HTTP transport; UI tests use local API fixtures. No tests send real email.

Run `make verify`, `make release-check`, and the PostgreSQL test with
`GATEWAY_TEST_DATABASE_URL` set to an isolated test database. Run
`scripts/ui/email-login.cjs` and `scripts/ui/email-invitations.cjs` in Chromium
and with `UI_BROWSER_ENGINE=webkit`. The invitation suite checks separate browser
contexts, grant resume, explicit acceptance, existing-user sign-in/decline,
revocation and ordinary signup. PostgreSQL tests use an in-memory mail sink to
check persisted encrypted context through restart, resend and recovery; they also
exercise expired, revoked and previously accepted invitations.

## Account email design

All delivered account emails use `internal/auth/templates/account.html`. The
shared layout is a light card with the configured email logo (when uploaded),
installation palette, short purpose-specific copy, expiry, reassurance and a copyable fallback
link. Confirmation explains that password setup comes next; recovery explains
that a new password is followed by sign-in. Subject, inbox preheader and body
copy are defined together in `internal/auth/email_template.go`. A nonempty site
name replaces the configured sender display name. When the site name is empty,
the configured sender name is preserved; unnamed senders use a bare email address
without angle brackets, as required by Resend. New account email purposes should
reuse this shell rather than add independent HTML strings.

The template uses presentation tables, system fonts, inline styles, a mobile
media query and an Outlook fixed-width wrapper. Essential content and actions
remain readable when styles or images are blocked. Plain-text mail contains the
same instructions, expiration and original action link. Dynamic text and URLs
are escaped by Go's HTML template engine. Unsupported purposes fail before send.

No logo is embedded in the repository or binary. Email images come only from
administrator uploads: the explicit email raster first, then a compatible raster
header logo. If no compatible upload exists, the configured site name is shown as
text; an unconfigured installation has neither image nor brand heading. Resend
sends uploaded images as [CID inline attachments](https://resend.com/docs/dashboard/emails/attachments#embed-images-using-cid),
so the template needs no remote image URL or tracking pixel. Delivery retries
reuse exactly the same HTML, text, image and idempotency key.

Generate local previews without sending email:

```sh
GATEWAY_EMAIL_PREVIEW_DIR=/tmp/gateway-email-preview go test ./internal/auth -run TestAccountEmailPreviews -count=1
node scripts/ui/account-emails.cjs
```

Set `UI_BROWSER_ENGINE=webkit` and the matching `UI_BROWSER_PATH` for WebKit.
Previews test desktop/mobile widths, image and style fallbacks, and both action
links. They do not emulate Gmail's conversation heuristics or Outlook's Word
renderer; actual inbox rendering still requires delivered-message testing.

## Login method controls

Google and email have independent enabled switches in **Administration → Settings → Login methods**.
Disabling a method blocks new sign-ins and registrations through that method, including OAuth callbacks
and email verification/password setup. It does not delete configuration, credentials, users, pending
challenges or active sessions. Re-enabling restores access; existing challenge expiry times still apply.
The switches are read from PostgreSQL on each authentication request and fail closed if settings cannot
be read. Email delivery remains available for notifications regardless of these switches.

With email sign-in enabled but no delivery provider, existing accounts can sign
in; registration and recovery actions are unavailable until delivery is configured.
The administrator card shows a warning. Both methods may be disabled;
existing administrator sessions and authorized API keys remain usable to restore them.

## Login provider administration

Use **Administration → Settings → Login methods** to connect Google or enable email sign-in. Google uses a web OAuth Client ID and Client Secret; register the displayed Redirect URI in Google Cloud. The callback is derived from `AUTH_PUBLIC_URL`, not request headers. See [Google web OAuth documentation](https://developers.google.com/identity/protocols/oauth2/web-server).

Login providers use independent versioned records. Google secrets are encrypted with the existing provider credentials key and never returned or audited. Updates apply without restart; an OAuth flow started before a provider update must be restarted. Existing sessions and account records remain intact.

Existing `GOOGLE_OAUTH_*` values seed an unconfigured Google connection once at startup, preserving its enabled state. After migration they can be removed; they never overwrite console changes. Keep `AUTH_PUBLIC_URL`, `SESSION_SIGNING_SECRET` and the credentials encryption key as infrastructure configuration. Email delivery has its own provider settings; its absence blocks new registration/recovery mail, not existing password sign-in.
