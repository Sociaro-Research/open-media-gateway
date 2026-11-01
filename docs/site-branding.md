# Site branding

Administrators can change the installation brand in **Administration → Settings →
Appearance**. Changes apply on the next page load across the console, sign-in,
public documentation, privacy and terms pages, and account emails. No build or
restart is required. Existing users retain their Dark, Light or System preference.

The editor provides site name, browser description, contact email, named color
themes with all shared palette colors for both modes, chart colors, full logo, compact logo and favicon
in matching Dark theme and Light theme columns, plus shared home screen and email
images. Colors use six-digit hex values. Hover colors, background glows and table headers derive from
these tokens. Third-party provider/Google marks and media previews keep their own
colors. Fonts, layout and the substantive legal text remain code-managed.

The preview is isolated from the active page. Its heading and right-aligned shared Dark/Light switch sit in one row inside
the preview card. The switch changes only that card. Choose Dark or Light to check text,
actions, switches and feedback. Expand either palette to edit its colors. Save
in the **Appearance** heading publishes identity, images and layout. The separate
**Color themes** heading saves the preset catalog. Each action preserves pending
edits in the other section. Secondary **Discard** sits beside Save in the section heading, restores only its
section's confirmed values, and is visible only while that section has changes.
Both actions reserve their space so fields and preview do not move. There is no Reset action.
Check contrast in both modes before saving.

## Color themes

Each named preset contains a complete dark and light palette. Fresh installations
ship with **Original** as the only preset and the site default. On reading a legacy
appearance record without a preset catalog, its exact existing colors become
**Original**. The generated catalog becomes durable with the next Appearance or
Color themes Save; reading old settings does not write to the database.

Saved catalogs, including previously saved Clear Lime and administrator-created
presets, retain their colors, names and default selection. Existing users retain
their saved color theme IDs. Administrators can create, edit, rename and delete
presets through the existing editor; upgrades do not remove themes.

The **Color theme** dropdown lists existing presets, marks the default with
**(default)** and initially selects that default. Its last option, **+ Create new theme**,
asks for a unique name and copies both palettes of the currently edited preset.
Cancelling creation retains that preset and does not create pending edits.
Choosing an existing preset selects the draft to edit and preview; changing
this selector alone does not change the active site or create unsaved edits.
Edits survive switching between draft presets. The selected preset's properties
include **Theme name**, the shared **Default** switch and **Delete** in
one desktop row with equal flexible gaps of at least 32px, distributed across the full content width and half its width reserved for the name, followed by the preview and both palettes. Narrow screens move
the name above the switch and deletion button. The editor omits introductory
help text and default-switch descriptions; the dropdown identifies the default. Enabling the default switch makes that preset the site
default. Disabling it assigns the previous persisted default when available,
otherwise another existing preset. The dropdown marker updates with the draft.
Exactly one preset remains the default for public pages, emails and users who
follow the site default.

Delete removes the selected preset from the draft and selects another preset.
Deleting the default also transfers its default status. Discard can undo this
until Save publishes the catalog. The last preset cannot be deleted or lose its
default status. Users whose saved selection is deleted fall back to Site default.

One primary **Save** appears beside **Color themes** only after a name, color,
default status or catalog membership changes. It publishes the whole catalog
atomically; an invalid palette in any preset blocks publication. Successful saves
use the shared three-second **Saved** feedback. Choosing an editor preset,
preview mode or preview switch does not create unsaved changes.
Names accept 1–80 characters without surrounding spaces or control characters,
and are unique without case distinctions. The catalog supports up to 32 presets.

Every user selects a published **Color theme** in **Profile → Appearance**, using
the existing inline Save. This choice is independent of **Theme** (Dark, Light,
System), persists across sessions and does not change another user's selection.
**Site default** follows later administrator changes. An unavailable saved ID
renders the default safely. Public documentation always follows the OS mode and
the installation default, ignoring personal console preferences.

Presets contain colors only. Site identity, images, widths and navigation settings
remain installation-wide. Additional optional roles are `accent-text`, `chart-1`,
`switch-thumb`, `switch-thumb-on` and `switch-on-border`; legacy fallback values
preserve the previous appearance exactly.

New installations have no site name, contact address, description or brand images.
These fields can stay empty. Browser titles use the functional label “Media gateway”
until a name is configured; legal text refers to “This service”. The shared base
palette is neutral, with semantic feedback and distinguishable chart colors so the
interface remains usable. Saved palettes and uploaded assets remain in PostgreSQL;
removing repository assets does not modify existing installation settings.

Static HTML contains no brand image sources or default favicons. The independent UI
loads public settings first and reveals header images only after the selected
upload has loaded. Slow or failed configuration/image requests leave the logo
empty rather than showing a placeholder brand. Theme changes use the same logic.
There is no bundled logo, symbol, favicon, phone icon or email logo in either the
frontend artifact or the core binary. Provider and sign-in service marks identify
third parties and are not installation branding.

## Images

Upload PNG, JPEG or plain SVG, up to 512 KiB each. Raster images are limited to
4096 pixels per dimension and 8 megapixels. SVG accepts only passive vector
shapes, text, gradients and local fragment references; export without scripts,
styles, animation or external resources. Home screen icons must be square PNG or
JPEG. Email logos also require PNG or JPEG for mail-client compatibility.

The image editor and color editor use the same order: **Dark theme** on the left,
**Light theme** on the right. Both image columns contain **Logo**, **Compact logo**
and **Favicon**, in that order. Each image preview uses its target theme's panel
color, so its contrast is visible before saving. Mobile stacks the same columns.
The source below each preview identifies an uploaded image, an inherited image
(including theme and role), or **No image uploaded**. Removing an upload resolves
only other administrator uploads; it does not delete another slot's image.

Header logos follow the site's effective theme, including a manually selected
profile theme and live OS changes in System mode. Browser favicons follow the
browser's system theme via two media-qualified icon links, independently of the
profile preference. Home screen icons are shared, because an existing phone
shortcut does not follow the site's theme setting. Account emails use a fixed
light canvas and one compatible raster image; they do not follow the profile theme.

Both theme slots are peers: each first uses its own upload, then the other theme's
upload for the same role. A full logo is absent when neither theme has an upload.
A compact logo can also inherit the favicon or full logo, in that order; a favicon
can inherit the compact or full logo. Within each role, the requested theme is
preferred. Without uploaded files these roles display no image.
The shared home screen icon prefers its explicit upload, then a favicon, compact
logo or full logo (light variant first within each role). For reliable home screen
use, upload a square PNG explicitly. Header images preserve their proportions.

Account email colors use the light palette. Email logo priority is the explicit
email logo, then a raster light-theme logo or dark-theme logo. When a new brand has
only SVG logos, emails show the site name as text until an email logo is uploaded.
No email logo is bundled. If neither a raster logo nor a site name is configured,
account emails use generic account instructions without a brand header. The sender display name
uses the site name; its verified address and delivery credentials remain in
**Email delivery**. Payment notifications use generic payment descriptions.

## Layout

Appearance includes a shared **Layout** group, independent of the color theme:

- **Maximum site width (px):** 1280 by default; an empty field uses the whole
  available desktop width. Both limited and unlimited sites retain at least 20px
  outer gutters. Numeric values accept 960–7680 CSS pixels.
- **Maximum content width (px):** empty by default; otherwise accepts 640–7680
  CSS pixels. Content is centered on the site when space permits; near the menu
  it shrinks without overlap. Individual controls and readable text can keep
  smaller functional limits. Mobile retains automatic width and 12px gutters.
- **Show subsections in the left menu:** on by default. When on, desktop Settings,
  Profile and organization details move their existing tabs into an expandable
  group under the parent section. Selection and drafts survive location changes.
  Mobile uses tabs in the content in either mode.

The optional API fields are `site_max_width`, `content_max_width` (integer or null)
and `sidebar_subsections` (boolean). JSON records without these fields inherit the
1280px site width and enabled sidebar subsections without a migration. Explicitly
saved navigation choices, including disabled subsections, are preserved. New
values use the existing versioned appearance record and appear in the branding audit event.
Width or navigation drafts apply to the site only after a successful save.

## Persistence and API

Migration `049_site_branding.sql` adds a singleton PostgreSQL record containing
configuration and image bytes, with its own optimistic version. Settings and
assets commit atomically. A stale save returns 409. Audit entries include palette
and image metadata, without copying the binary images into the log. Backups of
PostgreSQL therefore include the brand assets; provider storage credentials and
buckets are not involved.

The optional `presets` catalog and `default_preset` use the same JSON record and
optimistic version; top-level `dark` and `light` mirror the default preset.
Legacy writes without `presets` retain the catalog and update only its default
pair. Migration `052_color_presets.sql` adds `users.color_preset` (empty means
Site default) and includes it in profile audit events. `PATCH /v1/me` accepts
published IDs only; selection validates the catalog under a database lock.

- `GET /v1/system/branding`: administrator-only editor settings, defaults and color catalog.
- `PUT /v1/system/branding`: administrator-only complete replacement, with the current version; maximum request size 6 MiB.
- `GET /site.json`: public origins and appearance metadata, no-store. The independent UI applies theme tokens and layout settings; uploaded image bytes are served separately.
- `GET /branding/assets/{asset}`: public image bytes, no-cache and nosniff; SVG has a restrictive sandbox CSP. Asset URLs include the appearance version. The optional `symbol_light` and `favicon_light` keys use the existing JSON record; legacy unsuffixed keys are the dark slots, so no schema migration is required.

Public documentation exposes appearance metadata and public images. It does
not expose the administrator settings endpoint. The independent frontend uses text and DOM attributes for names, descriptions and
contact addresses; palette and asset formats are validated on save.
