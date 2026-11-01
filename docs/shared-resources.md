# Shared providers and storage

System administrators enable **shared providers and storage** through **Settings → System → Choose setup → Service for companies**. Its optional personal-provider control belongs to that same reviewed scenario; Advanced settings has no duplicate policy switches. Shared credentials belong to a stable installation identity; changing administrator roles never transfers or removes them. Administrators configure these connections under System. Personal connections remain under Configuration when personal provider access is enabled.

Every new API key must belong to an organization and one of its members. Provider and storage choices are immutable: use a new key for a different combination. The Generation screen creates a real key for the selected organization and remembers it in the tab for up to 24 hours.

## Ownership and funding

Organizations own reference metadata, generation records and files. All active members share the organization journal and reference library. They can reuse references, download results and delete stored files; financial administration remains restricted to administrators. The author and originating key remain recorded. New storage paths use `<organization-id>/references/` and `<organization-id>/results/`. Personal S3 connections may also hold organization-owned files; their original connection identity is retained for reads and deletion.

Shared-provider requests check the organization balance plus Credit Limit and the member weekly allowance against completed spending. Estimates and pending tasks do not reserve funds. Provider packages do not restore spending authority. See [Organizations](organizations.md) for funding, role, invitation and migration details. Personal provider calls are funded by the member's own provider account and do not debit the shared organization balance.

System shared reports retain date, user ID and key ID filters. Organization reports use `/v1/organizations/{org}/reports`; ordinary members receive only their own usage without provider settlement details. The current-key response includes its organization allowance. The former per-user and per-key budget endpoints are removed.

## Storage allowance

System → Shared storage has an optional **per-organization** size allowance. Blank means unlimited. Current usage includes live reference, result and last-frame bytes across all organization members and keys. Uploading increases usage; successful physical deletion decreases it. Existing shared objects are backfilled, with missing sizes reconciled from storage metadata.

New reference uploads and generation submissions are blocked only when current usage is strictly greater than the allowance. Equality is allowed, and incoming file sizes are not reserved. Already accepted tasks always save their results, even if they exceed the limit. Direct external S3 mutations are not continuously scanned.

The journal's **Delete result files** action removes stored results and last frames after confirmation. Metadata and accounting history remain. Deletion uses each object's original connection, including S3 version cleanup; partial failures are retryable. After deletion Gateway downloads stop, and any retained provider URL is shown as a potentially expired fallback.
