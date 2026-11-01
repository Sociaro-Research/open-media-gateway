# UI design system

## 1. Source of truth

Applies to every screen, dialog and popup. For every UI feature or fix, read this
document before implementation and compare the rendered result with the applicable
rules before completion. Reuse components; fix shared styles instead of adding
screen-specific copies. Exceptions must have a functional reason documented here.

- **Tokens and layout:** `web/public/ui/styles.css`.
- **Fields, validation and saving:** `web/public/ui/form-ui.js`.
- **People and avatars:** `web/public/ui/user-ui.js` (`UserUI.identity`, `avatar` and `option`).
- **Dropdowns and calendars:** `web/public/ui/control-pickers.js`.
- **Model selection and information:** `model-picker.js` and `model-info.js` in the same directory.

**Role names:** use **Administrator** for the system-wide role. Organization roles are **Supermanager**, **Manager** (`admin` in the API), **Content Officer**, **Developer** and **Member**. Sales account roles are **Regional client manager**, **Lead client manager** and **Client manager**, with scoped onboarding and statistics access. Reuse `SalesHierarchy.roles` for sales labels and selectors, and `OrganizationRoles` for organization labels and role selectors; the Role assignment heading reuses `ModelInfo.informationButton` for its permission matrix. Its wider popup is justified by the six-column comparison, with shared styles and horizontal table scrolling on mobile.

## 2. Visual foundations

| Element | Standard |
| --- | --- |
| Colors | Background `--bg` #0a0a0a; panel `--panel` #141414; field `--field` #101010; popup `--popup` #1c1c1c; border `--line` #303030. |
| Text and actions | Text `--text` #f5f5f5; secondary `--muted` #a3a3a3; accent `--accent` #e5e5e5; hover/focus `--highlight` #ffffff. Use tokens, not local color copies. |
| Feedback | Error `--red` #f08088; success `--success` #79d6a0; warning `--yellow` #f3c45b. Include text, not color alone. |
| Typography | Inherit the Inter/system font stack. Page/section/group headings: 36/20/16px; fields/body: 14px; tables: 13px; secondary detail: 12px. Monospace only for code. |
| Spacing | Sections: `--space-section` 32px. Heading-to-content: 24/20/16px for page/section/group; shared action heading: 24px. Field groups: 12–20px; panel padding: 24px, 20px on narrow screens. |
| Shape | Fields and popups: 12px radius, 1px border. Use existing shared classes; do not invent per-screen radii or shadows. |

### Spacing and visual review gate

Spacing is a required acceptance criterion for every UI feature and fix. Build a
harmonious hierarchy between components using the existing values above and the
shared CSS patterns; do not introduce a second spacing scale. Use `--space-section`
for section separation, the existing heading-to-content distances for each heading
level, and the shared field grids and panel padding for grouped content. Shared
action groups and switch labels use 12px gaps. Keep the shared 42×24px switch and
its label vertically centered, with clear separation from neighboring actions.

Prefer the parent's `gap` for sibling spacing. Avoid accumulating child margins
and parent gaps, collapsed gaps around text nodes, or unintended empty grid tracks.
Preserve the shared `FormUI` reserved action space (`.stable-action`) that prevents
layout shifts when Save/Cancel is hidden. Repeated groups must use the same spacing and alignment. Keep feedback
within its group; showing Save, validation, loading or error states must not cause
overlaps or erase the separation between controls. On narrow screens, wrap or stack
groups in reading order, retain label/control gaps and use the existing mobile
panel gutters. Do not squeeze controls together to make a row fit.

Before accepting any UI change:

- Identify the applicable rules here and their shared CSS/component sources.
- Capture and inspect before/after rendered screenshots with safe local fixtures.
  Compare section, heading, field, label/switch, action and panel-edge spacing;
  check alignment, wrapping and nearby screens that reuse the changed pattern.
- Inspect both themes at 1440/390/320px, plus any relevant layout breakpoint.
  Exercise keyboard focus and activation, dirty/saved, enabled/disabled, pending,
  error/retry and interrupted/repeated operations where applicable.
- Record concrete design comparisons, screenshots, executed checks and limits in
  ignored `.local/` or `dist/`. Passing tests or source review alone is not visual
  acceptance. Report any uninspected state rather than marking the gate complete.

**Themes:** Profile offers Dark, Light and System (default). System follows live OS changes. The independent **Color theme** field chooses an administrator-published preset or **Site default**. Each preset contains both palettes; switching the display mode retains the preset. Both preferences are saved per user. The separate Color themes section creates, names, edits and deletes presets. Its Color theme dropdown initially selects the default, marks it with (default), and ends with + Create new theme. The selected preset has its name, shared Default switch and Delete in one desktop row, with the name occupying half the row and equal flexible gaps of at least 32px, distributed across the full content width, wrapping on narrow screens. The isolated preview contains its heading on the left and shared Dark/Light switch aligned to the right edge in one row. Keep these controls self-explanatory without introductory help text. One versioned Save in that section heading publishes the entire catalog independently of identity, images and layout; both sections preserve the other section's pending edits. Delete is reversible until Save, and the last preset remains the default. Fresh installations ship only **Original**, selected as the default. Legacy colors become **Original** without changing their values. Saved catalogs and user selections are preserved; administrators can create additional presets. Identity, images and layout are shared across presets. Apply the selected palette to every surface, control, table and popup. Light tokens live in `:root[data-theme="light"]`; never add screen-specific theme overrides. Media previews preserve their original colors. Provider logos use their original pixels in dark mode and black RGB with unchanged alpha in light mode.

Use `--accent` for filled actions and on switches, `--accent-text` for accent
foreground text; focus/selection edges use `--accent`, and `--chart-1` for the first chart
series. The latter two inherit `--accent` in legacy palettes. Switch off/on
thumbs use `--switch-thumb` / `--switch-thumb-on`; both inherit `--on-accent` in
legacy palettes. The on edge uses `--switch-on-border`, inheriting `--accent`.
Separate roles allow a bright action fill, readable light-theme text and visible
switches without changing the original palette. All color values remain shared
tokens, editable in both palettes.

The Administrator's Monitoring action reuses the existing sidebar button. It
opens Grafana as a separate application at `/monitoring/`, using the same gateway
session. Grafana retains its native dashboard grid, time picker, chart controls,
themes and accessibility behavior: these controls belong to an independently
maintained application and cannot use the console's DOM components. This is a
functional exception, not a second implementation of console controls. Validate
its desktop/mobile layout and keyboard navigation separately. The console's
palette and content security policy remain unchanged outside this path; Grafana
supplies its own nonce-based content security policy.

The standalone public documentation always follows the OS theme, including live
changes, using the same shared palette. Its root declares
`data-theme-mode="system"` before the shared theme script runs. It has no theme
selector, uses the installation's default color preset, and neither reads nor
writes the console's saved mode or color preset preference,
including when served at `/docs` on the same origin during local development.

## 3. Controls

The first-owner setup uses the existing sign-in card, `FormUI` floating fields,
password visibility controls, validation messages and primary action. Setup code
and password stay out of browser storage. No design-system exception is needed;
validate the same 1440/390/320 px layouts and keyboard controls as email sign-in.

- **Inputs:** use `FormUI` floating labels **inside** the border. The label serves as the empty-field prompt and moves up on focus/value; no duplicate placeholder. Standard height: 58px; value: 14px; raised label: 11px. Textareas use the same frame and label, growing with content.
- **Credential fields:** use the same internal floating label. Mark optional billing credentials with `(optional)` in that label. Show the saved mask and available last four characters inside the input; never repeat them below it or add `Not set`. Untouched masks preserve secrets; cleared provider credentials are deleted on save.
- **Dropdowns:** use `ControlPickers`, with the same frame, label and height as inputs. **Table variant:** 44px, no visible label because the column heading supplies context; retain an accessible label. Do not use `data-plain-control` to bypass standard field styling.
- **Searchable people selection:** use `ControlPickers.search` with the search field inside the dropdown, above scrollable results. Do not add a separate search field or Search button to the form. Keep avatars in results and the selected value; keep loading, empty, retry and pagination guidance inside the popup. Preserve selection while searching and ignore stale responses after another query or dismissal. Support keyboard navigation and Escape without submitting the parent form.
- **Multiple selection:** use the shared `ControlPickers` multiple selector. Keep the popup open while changing options, use the shared switches, and support arrow keys, Space, Tab and Escape.
- **Duration:** Templates and Playground use `ModelInfo.durationLabel` to show the selected model's known seconds bounds directly in the floating input label. Include a non-unit step when the allowed values are discrete, show only a known one-sided bound, and retain honest unavailable/input-derived states. Do not repeat the range in a detached caption. Preserve native bounds/step validation, saved-value reconciliation and distinct validation errors. Give the label enough field width to remain readable; the narrow Playground panel places Duration on a full row.
- **On/off switches:** every checkbox uses the shared oval switch (42×24px), with a 16px thumb: left on `--switch-off` when off, right on `--accent` when on. The thumb and on edge use the shared roles above. The same size and style apply to forms, tables, permissions and provider lists; no square ticks or Enable/Disable buttons. Keep an accessible label, Space-key activation, visible focus and disabled states. Preserve each form’s save behavior; immediate changes lock while saving and restore the confirmed state on failure. Radio buttons remain for mutually exclusive choices.
- **Dates/months:** use the shared calendar, including its icon, navigation, selection, Clear/Today actions and keyboard controls. Never introduce a native browser calendar as an alternative.
- **Model selection:** always use `ModelPicker`: provider groups, model names, information button; internal name search, release-date cutoff and content-type filter. Provider headings use 32px square artwork, 16px bold names, at least 64px height and 16px separation with a shared divider between groups. Every provider logo uses the original PNG in dark theme and `brightness(0)` in light theme, preserving transparency and antialiased edges. Missing or failed artwork uses an initial in the same slot. Long names wrap; distinct connections retain separate headings even with equal display names. Filters combine and never select a model automatically. Keep filters in one row on wide screens and stack them on narrow screens; keep them visible while results scroll.
- **Scrollbars:** one global style: transparent track/corner; 8px width; rounded thumb mixing accent 45% with border color; accent on hover. Firefox uses the shared thin scrollbar equivalent. No local overrides.
- **Provider settings:** show connection settings in expanded cards using the shared provider header, availability switch and field grid. Administrators immediately upload provider logos through the shared `ReferenceUpload` dropzone, separate from credential drafts. Hide native file-input chrome; reuse its styled keyboard picker, drag/drop, small muted hint and progress/error/retry states. Show a round 44px remove control beside installed artwork; removal persists immediately, retains credential drafts and restores the no-logo state. Accept only decoded transparent PNG, up to 1 MiB and 2048 pixels per side; retain the confirmed image and show local retry feedback after failure. The compact square preview keeps the remove action beside it; upload actions and the 11px format hint wrap within the shared dropzone on narrow screens. Use an unlabeled header switch with an accessible provider-specific name. Payment modes are **Production** and **Test**. Place credential validation beside the relevant field in `.field-action-row.control-row`; validation of saved credentials is unavailable while that credential has unsaved changes. Put settings shared by all providers above the cards; avoid Configure buttons and repeated scope explanations.
- **Payment provider actions:** the form includes its shared card header. Save
  and the new-provider Cancel use `.form-heading-actions` beside the immediate
  availability switch, with equal 40px heights and 12px gaps. Save remains
  centered and compact, with the normal dirty/Saved states; availability changes
  do not dirty the settings draft. Wrap the action group below the title on
  narrow screens. Add network uses `.form-actions`, fitting its label on desktop
  and the shared responsive layout on mobile. Remove uses the table action and
  `.control-row` styles; inputs, selectors and row buttons are exactly 44px high.
  Direct provider-form explanations/table wrappers have no extra margins beyond
  the form grid's spacing. No visual exception is required.
- **Control rows:** inputs, dropdowns and adjacent buttons must have the same height and vertical alignment: 58px normally, 44px in tables. Use `.control-row`; use the shared heading row to align a heading with its action. Repeated editable records use a table with column headings, without duplicate per-row summaries.
- **Modal layout:** use `FormUI.dialogHeading`: title at the top-left, visible × at the top-right, with equal top/left padding (24px, 20px on narrow screens). Reset a direct modal form's page-level top margin. Keep the icon on the title row with a 44px touch target and an accessible Close label. The icon and Escape dismiss the draft without applying it; block dismissal during a write. Do not put a stray Cancel or text Close button in the body.
- **Modal actions:** put actions in the shared `.dialog-actions` footer. One action aligns right; two actions occupy the left and right sides. A modal that reviews a pending change uses **Apply**, not the page-settings Save pattern. Use `FormUI.watch(..., {placement:'footer'})`: the action stays in place and is disabled until the draft is changed and valid. Close the modal after successful application. Do not move its action into the heading or show a separate Saved phase in a closing modal. Distinct create/payment/destructive actions retain their concrete labels. Independent forms in a multi-operation dialog retain their separate transaction boundaries, with actions below their own fields.
- **Legal document editor:** uses a plain multiline field and the shared modal heading/footer. Save publishes directly; Cancel explicitly discards the draft, as requested for these long public texts. The footer therefore uses Save/Cancel rather than Apply, with Close/Escape also discarding. Dismissal is blocked during saving.
- **People:** every displayed person reference includes their avatar next to their name/email, on every screen and in every dialog, row, summary, notification and selector. Use `UserUI.identity`; use `UserUI.option` with `ControlPickers` so both the selected value and popup choices have avatars. Use the existing account picture when supplied by the authorized response; missing, failed or unsafe images show initials in the same slot. Never fetch an unscoped user directory to decorate a name. Editable name/email inputs, accessible-only control labels and verbatim JSON/CSV records are data rather than rendered identity references; preserve their native editing/export semantics.
- **Buttons and states:** reuse primary/secondary actions and shared hover, focus, disabled, validation and saving states. Do not communicate availability through color alone.

## 4. Saving and feedback

- Single-value inline editors: show Save **inside the field on the right** only when changed; reserve text space and keep row height stable. Reuse `.member-limit-form` / `.role-editor` behavior and `FormUI.watch`.
- Multi-field page settings: one save action in the shared section heading. The heading and form share the same content width; align the action with the form’s right edge. A table row editing several values may use an unlabeled action cell to save them together. Modal forms follow the footer rule above.
- Optional secondary **Discard** sits immediately before Save in the shared heading action group. `FormUI.watch` reserves both buttons' space, hides Discard when unchanged, and uses the same baseline; discard restores only its section. Do not add a generic Reset to the appearance editor.
- **Profile contact drafts:** keep the section Save visible and enabled whenever the contact list changes, including incomplete or invalid rows. Validate on Save with the shared field error and focus behavior, retain the draft and send no invalid write. A plain email address is stored as a safely encoded `mailto:` link; other entries require an explicit supported URI. Pending writes still lock the form.
- **Page Save/Apply feedback:** show the action only for unsaved changes. After a successful response, replace that exact button with green **Saved** for three seconds, keeping its position and size; then hide it. Use `FormUI.saved`, never a separate success message elsewhere. New edits restore Save/Apply immediately. A modal that closes on success uses its stable footer action instead.
- While saving, prevent duplicate submission. Accept only the submitted values as the new baseline; preserve newer edits. On failure, retain edits and show the error beside the affected form. Immediate switches, validation and create flows (Create, Assign, Add credit) are distinct actions, not Save/Apply feedback. Password setup/reset opens sign-in with instructions; removing your own access immediately closes the protected view.
- Use `FormUI.sectionHeading` for action headings and `FormUI.message` for feedback. Keep spacing stable when messages appear.
- **Automatic data updates:** never show Refresh/Reload buttons or ask users to reload a screen to obtain current data. Update after successful actions, on returning to the screen and automatically while it is visible. Retry transient read failures automatically. Background reads must not clear content, flicker, steal focus, interrupt dragging or replace an open draft. Ignore stale responses, stop updates on navigation and pause them in hidden tabs. Resolve version conflicts by retrieving the current state and asking the user to review it before another write; never repeat a conflicting write automatically.

## 5. Tables

- Use `.docs-table` and its shared styles in `docs.css`, including model settings and documentation. All headers form one continuous `--table-header` strip; no borders between rows or cells. Keep columns aligned and wide tables horizontally scrollable.
- Highlight a row and show a pointer only when clicking it has a default action. Support Enter/Space for that action; clicks on controls must not trigger it.
- Leave action-only column headings blank. Put optional actions (Manage, Remove, Revoke, receipt/invoice links) in `.table-row-actions`: reveal on row hover or keyboard focus; always show on touch devices. Reserve their space to prevent layout shifts.
- Save is **not** a hover action: use `FormUI.watch`, hide it for unchanged rows, show it only for unsaved changes, and reset the baseline after success. Keep pending actions locked and preserve edits on failure.
- Controls and buttons in a row share the 44px table height. Data-only and editable rows have no hover background unless they also have an explicit default action.

## 6. Usage and Reports

Keep Usage and Reports as separate sections with the same filter controls. Usage shows metrics, spend over time, model rankings and provider breakdown; Reports shows only the filtered request table and CSV export. Report rows open a read-only generation detail dialog with every retained field permitted for the viewer. Use the shared interactive row states, Enter/Space activation and native dialog focus/Escape behavior. Show full prompt text and saved settings without truncation; result and reference links use authenticated or existing signed routes. The wider record dialog is justified by structured settings and long request content, with the shared palette, spacing and mobile gutters.

Charts use the shared accent and `--chart-*` tokens in both themes. Keep exact values accessible on hover and keyboard focus; provide a table for the provider chart. Scope all aggregates to the applied Usage filters; show missing measurements explicitly.

Reports opens its shared multiple-column picker from a gear button in the last
table header cell. Keep that cell visible at the right edge during horizontal
scrolling; preserve the picker and keyboard focus while saving preferences.
Generation cards show reference and result images as compact previews of the
original files, with video/audio players for those media types. Use short Open
and Download actions and the shared copy-link button instead of displaying URLs.
Unavailable previews keep these actions; content visibility still follows the
report's permissions and retention policy. Shared `ControlPickers.enhance` accepts
an existing icon button as its trigger without creating a second dropdown field.

## 7. Review before completion

Audit Log reuses shared filters, calendars, tables and the read-only detail
dialog. Its Events / Revoked keys tabs follow the shared subsection behavior.
Inside an organization they remain horizontal as a nested tab group, avoiding
a second level of sidebar navigation; `GatewayLayout.bind({inline:true})`
retains shared roles, focus and keyboard behavior.

Check actual desktop and 320–390px layouts, keyboard/focus/Escape, empty and filtered lists, nested popups and scrolling. Compare controls with the shared implementations. Extend a shared component when needed; do not create a visual exception to bypass it.

## 8. Advanced API configuration

Provider connections reuse shared fields, switches, tables, dialogs and model
pickers. JSON textareas are reserved for administrator-defined capability schemas
and optional request-body fields whose shape depends on the API; normal creation,
credentials, headers, URLs and prices use labeled controls. Picker popovers and
model information opened from a modal must be attached inside that dialog so they
remain interactive within its modal focus boundary.

## 9. Installation branding

The base palette is neutral and contains no installation identity. Brand images
are absent until uploaded; static HTML must never contain a default logo source.
Missing or loading settings leave header images empty, and only loaded uploads
are revealed. Names and contact addresses are optional and have no company defaults.

Administrators can override every shared palette token in Appearance, separately
for dark and light themes. `/ui/site.js` reads `/site.json` and applies the configured theme tokens; all
brand-colored surfaces, glows, feedback and charts use these tokens. Images and
colors share matching Dark theme / Light theme columns. Each image column has
identical Logo, Compact logo and Favicon slots; phone and email images form an
explicit Shared images block. Preview backgrounds follow their target palette,
and every image identifies its resolved source. Header images follow the site
theme; favicons follow the browser system theme. Brand images
fit the existing header height while preserving their own aspect ratios.
Appearance uses shared floating fields, action headings, saves, feedback and the
shared theme dropdown. Native color inputs complement editable hex fields: this
is a justified exception because the browser provides a keyboard-accessible color
chooser. File inputs use the native accessible upload control. The isolated
preview changes only its own tokens until the administrator saves. See
[site-branding.md](site-branding.md) for scope, images and fallback behavior.

Installation layout uses two shared desktop maximums: the site frame (1280px by
default) and the working content frame (unlimited by default). Empty values remove
the corresponding limit. Both bounded and unlimited sites retain a minimum of
20px outer gutters. Content centers on the site when both sides have enough
room for navigation, otherwise it uses the available space beside the menu.
Mobile retains 12px gutters and automatic widths. Container queries adapt shared
forms and grids to a narrow content frame even on a wide monitor. Compact controls,
readable documentation and profile forms retain their functional local limits.

`layout.js` manages subsection navigation for Settings, Profile and organization
details. By default, desktop navigation moves the same
controls into an expandable group under their parent in the left menu, preserving
selection and form drafts. Parents remain neutral; only the selected subsection
uses accent text and a straight 2px segment on the shared thin vertical divider
to the left of the subsection group. This reuses the header navigation's line
style in a vertical orientation, without a selection fill or rounded marker.
Sections without subsections keep their existing selection
style. Vertical tabs use Up/Down, Home/End and Left/Escape to
collapse; Right on the parent expands and focuses the selected tab. Horizontal
tabs use Left/Right and Home/End. Disabling sidebar subsections restores horizontal
tabs on desktop. Mobile always restores horizontal tabs. Code
example language selectors and independent lists are not navigation subsections.

## 10. Catalogue synchronization

Catalogue settings reuse floating fields, the shared timezone dropdown, switches,
action headings and versioned Save feedback. Sources are an editable shared table
with labeled table dropdowns; scroll horizontally inside the table on narrow
screens. The shared settings form grid uses a zero minimum track to prevent wide
tables from expanding the page. The journal reuses filters, tables, hover/focus
actions, pagination and native system dialogs. Warning text uses the shared
`report-warning` token state. Evidence is preformatted JSON with wrapping and
bounded scrolling, a functional exception for an administrator inspecting exact
provider data; ordinary settings are labeled controls. Source snapshots remain
read-only, escaped text. Verify both themes, 320/390px layouts, keyboard actions,
nested snapshot dialogs and Escape.

Timezone choices lead with standard UTC offset, sorted numerically from UTC−12 to
UTC+14, and include fractional offsets and city examples in parentheses. Group
equivalent IANA regions/aliases rather than listing individual cities. Keep
different daylight-saving rules separate and preserve existing saved identifiers.
Use the same human-readable label in provider schedule summaries. The generated
`timezones.js` catalogue feeds the existing shared dropdown and keyboard behavior.

## Sales hierarchy

Sales management uses nested lists and employee cards instead of a table because
reporting relationships, vacant ranks and drag destinations must remain visible.
Cards use existing panel, line, muted-text and focus tokens. Role/supervisor
editors reuse `FormUI` and shared labeled dropdowns in a native dialog. A role
change is reviewed before Apply when it relocates incompatible direct reports,
fills a vacancy or starts an empty branch. The dialog closes after a successful
move because the updated tree is the confirmation. Ordinary same-role supervisor
drops are immediate actions.

Each skipped rank with existing descendants has a vacancy target that adopts
that unled branch atomically. Separate New team (lead) and New region (regional)
targets create empty branches: the employee moves alone, while former direct
reports stay with their previous higher supervisor or Administrator. Keep current
team preserves compatible reports during ordinary moves. The editor's Team
selector provides the same choice for keyboard/touch users. Vacancy adoption and
starting an empty branch must work even with unchanged role/supervisor.

Role slots inside the dragged employee's own branch resolve to their nearest
eligible external supervisor; never propose self-reporting. On demotion,
incompatible reports individually move up the destination supervisor chain until
their new supervisor has a strictly higher rank, falling back to Administrator.
Do not render an isolated ordinary-manager slot at the root when no ordinary
managers report there; retain slots inside their actual branches.

Every drag operation has a
keyboard/touch alternative: activate the employee's avatar/name area with a click,
tap, Enter or Space to open the role editor. Use a native button styled as the
identity itself, without a separate Move / change role action. Keep Permissions
and the drag handle independent. At 390/320px, actions stack
inside the card; indentation is reduced without flattening the hierarchy.

Single-employee add/role editors use a compact 420px dialog with the shared
24px outer padding (20px on narrow screens). The selected employee occupies a
full-width identity card with 16px inner padding, a 40px avatar and a 12px gap
before the name/email. Fields follow the same 16px vertical rhythm, without
inherited label margins. Affected team members appear below their explanation
as a semantic list: one avatar/name row per person, 32px avatars and 12px row/gap
spacing. Never embed a run of people inside the explanatory sentence. Larger
multi-operation permission and bonus dialogs retain their own content width.


Sales permission dialogs reuse the hierarchy dialog, shared on/off switches,
floating fields, employee dropdown and Save feedback. The independent
forms show grant issuer/time; permissions are boolean, without top-up amount caps.
Saved/revoked permissions are reloaded together so dependent revocations are
visible immediately. Company commercial settings reuse the existing markup and
model-picker components. Mobile employee-card actions occupy the second grid
column; no action expands the page at 320px.

Sales bonuses reuse the sales native dialog, shared labeled selectors/calendar,
number fields, dirty-save controls, status messages and form spacing. Balances use
USD formatting; negative balances use the existing error color. Payout forms are
shown only for authorized subordinates, retain an operation ID across retries and
block dialog dismissal while saving. Global share fields belong in Administration
→ Settings → Sales, separate from the employee tree and individual bonus balances.
Use the shared `.keys-fields` grid: two equal columns on desktop, stacked on narrow
screens, with the same inline Save behavior as other settings.
Usage adds a Profit/Spend button group to the existing
chart, with signed bars and pending-cost counts; customer views omit this control.

Sales hierarchy employee cards reuse the shared 32px circular `.user-avatar`
style next to the name, email and role. Show the account profile picture, with
initials while absent/loading or after a load failure. Images are decorative
beside the employee name and are not draggable; the existing move handle remains
the drag source. The identity row and its text group shrink/wrap on mobile.

Each vacant sales level is one outlined drop row containing the role and vacant
state together. Its skipped-level descendants remain nested below that same row.
Do not render a separate drop prompt and a second label for the same vacant level.

Company creation requires the same labeled website field in Administrator and
sales forms. Duplicate errors remain beside the form and retain its draft. Use
`FormUI.valid(form)` for passive readiness checks; reserve `reportValidity()` for
explicit submission so incomplete fields never steal focus during ordinary entry.
The company creation modal uses the shared top-right close header.

Stablecoin payment settings reuse expanded provider cards, the shared immediate
header switch, table dropdowns and token multiple selector. Receiving wallets are
editable table records in a horizontally scrollable shared table on narrow
screens. Transfer instructions reuse card/field/copy styles and a two-column
amount breakdown; the QR is generated locally and displays the receiving address
only, with the network and token stated in adjacent text. The payment form hides
after creation; reopening instructions uses the same card. No design exception.

Seller company and VAT settings use a shared multi-field section with a heading
Save, two-column/stacked identity fields and an optional VAT number, plus an
editable table for country/status/treatment/rate rules. Buyer VAT verification
uses the same section pattern in organization Billing and is editable only by
Administrators. Expiry guidance is textual; no alternate date control is added.


### Company invoice settings and downloads

Seller company settings reuse the shared floating fields, country selector,
section Save and existing branding image preview/upload/remove classes for a
separate invoice logo. A blank VAT number means no VAT registration; there is no
duplicate registration switch or disabled VAT input. Show OSS only when a VAT
number is supplied. Format errors use shared field validation and retain the
draft; server errors for that number return to the same field. This is format
validation, not confirmation of tax registration.

Shared settings groups use 24px from the action heading to content, 32px between
groups, 20px between fields and 16px between a group heading, explanation and
table. Grid headings and table wrappers have no additional inherited margins.
The shared inline branding asset places a fixed 96px square preview on the left
and its title, concise format hint and actions on the right. Images preserve
their aspect ratio. Actions retain 44px targets with 12px gaps and wrap on narrow
screens; the preview never becomes a full-width banner. Without an image, omit
the preview and Remove action. Empty VAT rules omit the unused table header.

The entire company/rules/logo draft shares
one versioned Save; uploading or removing a logo alone makes it dirty. The Stripe
card adds the shared **Use Stripe Invoices** switch to its saved draft. New
payment-only card purchases reuse the company-billing fields already used by
stablecoins, with token/network controls hidden and disabled.

Local **Invoice** actions are shared secondary buttons in payment rows, history
and the stablecoin card. They download through the authenticated API (including
API-key mode), lock against duplicate clicks and show errors in the section's
existing status region. Native Stripe invoices retain their external link.
The exported invoice intentionally uses the user's A4 document design and a
white paper background, independently of console colors and the site logo.


PDF invoices embed Liberation Sans Regular/Bold to match the supplied
Arial/Helvetica document style. Payment hyperlinks use conventional blue
(`#185ab4`) with an underline, while adjacent payment labels and expiry guidance
retain their standard document colors. This is the exported document's existing
paper-design exception; console theme tokens and links are unchanged.

### Company support and personal contacts

Every company role has a Support section. Its client manager card reuses
`UserUI.identity`, shared panel tokens and secondary link actions, with responsive
wrapping. Empty assignment/contact states explain the next step. Only deliberately
published links are shown; account email is not inferred as a contact method.
Profile Contact links uses a heading Save, shared floating fields and an editable
Label/Link table with Add contact and Remove actions. The table scrolls within
its container on narrow screens. Optional-sharing guidance names its audience.
Member rows reuse the same contact-link renderer below the shared person identity.
No design-system exception is required.


Payment request **Expired** is a terminal state distinct from a short Stripe
Checkout session expiring. Expired stablecoin cards show a clear instruction to
create a new payment and omit QR/send/delete controls. New invoice issuance and
payment continuation are unavailable after the deadline; an existing document
remains downloadable. Active card requests reuse the shared secondary payment
action, including when Checkout needs renewal. The PDF always prints the common
request deadline and a separate fixed exchange-rate row for stablecoins.


## Prompt reference previews

The prompt remains a native `FormUI` textarea for input, selection, undo, IME and
plain-text submission. A pointer-transparent mirror paints colored media markers
beneath its transparent text, matching font, padding, wrapping and scroll. This
small rendering exception avoids replacing the editor with editable HTML. Use
shared color tokens and the existing model-info popover style. Hover, touch
pinning/close, Alt+Down and Escape provide equivalent preview access; missing
references get a red dotted state and a live message listing missing numbers.
Use the common UI reference forms for every model; never infer syntax from
its display name or informational provider advice. Image, video and audio
numbers refer to their own ordered input lists. Model capabilities still
control supported media and workflows. Video/audio previews require explicit playback and do not autoplay.
See [supported prompt reference syntax](prompt-reference-syntax.md).
Use native field validity and the shared validation message to block the
Playground form until corrected; do not add this restriction to the API. The picker reuses shared floating fields and
ControlPickers for source/group selection. Check 1440/390/320px, long scrolling
text, both themes, keyboard and both browser engines after changing this layer.

Model lists keep distinct native versions visible. The shared ModelPicker derives
unique display labels within its supplied catalog, adding native model ID,
provider/connection name and then connection ID only when needed. Case/whitespace
collisions and literal names resembling those qualifiers are resolved
in stable identity order. These are presentation labels: catalog IDs, public API
names, prices, routing and stored names remain unchanged. Provider model tables
reuse the same naming helper; filtering does not rename the remaining choices.

## Playground project actions

The Journal's shared Project dropdown contains **All Projects**, **No Projects**, **New Project…**, a divider, then existing projects. New Project opens the existing creation dialog without changing the filter; cancelling retains the filter. No Projects filters unassigned history and never becomes a submitted project identity. Keep the field 240px wide where space permits, shrinking to available width, with shared ellipsis and full accessible text. A real selected project shows **Edit** inside the field at the right, vertically centered; it opens Rename/Delete independently of the combobox. All/No Projects hide Edit. Keep Edit a sibling of the combobox button, never nested interactive markup. No external plus or ellipsis buttons remain.
The internal Edit is a plain text action without normal button chrome. Retain its 44px touch target and placement; use a text underline for keyboard focus rather than a border. Its Rename/Delete menu matches the complete parent field width and uses small monochrome icons in the shared SVG style. The generic menu opt-in must not resize unrelated menus. Shared dropdown labels align with the selected-value content edge, including the reserved layout border; focus must not shift either edge. Menu dividers use the same shared horizontal content gutters as option text and checkmarks.
Reuse `FormUI.dialogHeading`, floating fields and the shared compact actions.
Deletion requires confirmation and clears tags only, retaining generations/media.

A generation's **Project** action opens the shared `ControlPickers` action menu
beside the parent menu on hover, click/touch or Right Arrow. The current project
has a menu checkmark; choosing it again clears the assignment. These are ARIA
menuitemcheckbox actions rather than form checkboxes, so their checkmarks express
the user's explicit menu interaction. Arrow keys/Home/End navigate, Space/Enter
select, Left/Escape return to the trigger and outside click dismisses. Menus flip
and clamp to the viewport and use the same tokens, popup surface and focus states
as shared pickers. No project names are shown across key/workspace changes.

Unavailable generation actions remain visible but disabled with an explanation;
company/workspace/content-role permissions are preserved. Company projects and
assignment are shared across authors and keys; Profile projects remain private.
Duplicate legacy names use a stable project-ID suffix in selectors (full ID on
short-prefix collision), without renaming stored projects. Read-only roles cannot
create, rename, delete or assign. Generation content and media-deletion permissions
remain unchanged. Conflicts show the
confirmed current assignment and require another explicit choice. Preserve
Dark/Light/System themes and the generation form's pending values.
Renaming the selected project or changing generation project tags reconciles the
Journal in place. Metadata-only changes retain attached cards, scroll position,
keyboard focus and playing media, including the changed generation. Keep a
surviving visible card anchored when filtered membership changes. A reader at the
top still sees newly arriving generations. Explicit filter/key/workspace changes
replace the context; excluded generations and lost content permissions dispose
their media. No manual refresh control is needed.
The active assignment menu follows its same authorized task across per-card
polling refreshes, preserving conflict guidance and keyboard focus while
rebinding to the new anchor. Task removal, permission loss, explicit dismissal
or key/workspace change closes it; polling and real load errors remain active.

## Setup and remote workspace

The setup guide uses a native dialog/fieldset/radios, shared FormUI heading,
fields, switches and compact actions. Three scenario cards express onboarding
intent while personal/shared policy remains independent. Sociaro opens a remote
workspace dialog with the shared ModelPicker and explicit estimate confirmation.
Remote content, balance and history remain visibly labeled as remote; never
reuse the local company balance or silently switch paid providers. Use the
shared theme, modal scrollbars and 1440/390/320 px layout. There is no new design
system exception.

Installation authorization uses the existing trusted sign-in card and shared
native consent dialogs, floating fields, company dropdowns and switches. Label
the requesting installation origin as reported data and require explicit code
matching/consent. Preserve the remote/local workspace distinction and use the
existing company funding controls. The new primary flow never collects a remote
password in the local portal; the existing-key option is an advanced fallback.

## Reference libraries

`reference-library.js` owns the common dialog, cards, selected state, media
previews and permanent-delete confirmation, using the Playground design in
`references.css`. Playground and Templates configure this component rather than
copying its markup. Transport adapters retain their existing authorization and
model/slot validation. Never reconstruct media paths from the current owner;
render the URL returned by the authorized library response.

Playground keeps its multiple-reference selection and provider-specific library
adapter. Template user slots select one compatible organization reference. Remove clears the current binding before another file is chosen. Template editing uses the instance template library and administrator
mutation endpoints. Uploading to that library does not assign a slot. Delete
cancellation or rejection retains the selection; confirmed organization-file
deletion clears matching retained selections in both screens. Native audio/video
preview controls sit outside the selection button so playback cannot assign a
reference. Video and audio library cards expose a keyboard-accessible Play overlay
on their thumbnail/placeholder, with a 44px target and visible focus; do not add a
detached Preview action or Rename UI. Names and existing APIs remain unchanged.
Selected and focused cards use the configured `--accent`, rather than a fixed blue or a copy of the current theme color. Selection and keyboard focus share one perimeter; the focused selected edge is thicker. Preview
failures offer an overlay retry without changing the binding.

Single-file selection controls reuse `ReferenceUpload` with `onRemove`. Empty
blocks show Upload and Choose from library when that scope has a library. Selected
blocks show only Remove as a selection action, inside the block; hide upload,
library and replacement actions until removal. Remove clears the binding, never
the library file. For a fixed Template reference this makes the slot available to
the template user. Playback is separate from selection. Keep upload/validation
feedback, pending guards and keyboard focus through remove/select/retry and
navigation. Playground's multiple-reference input retains a separate empty block
for adding files; each selected file card has its own internal Remove action.

## Templates

The shared gallery reuses cards, fields, `ModelPicker`, reference-library dialogs,
`FormUI` heading Save, existing key/budget controls and generation confirmation.
The left panel contains the Playground key control, selected template details,
missing slots and generation action. The right gallery uses three, two or one
columns as available width decreases, and stacks below the panel on narrow
screens. All roles share this screen; existing edit permission adds inline Create
and Edit actions. Create Template and Template References share the page heading row, aligned right; the first gallery row aligns with the Use Template panel. On narrow screens heading actions wrap with the shared 12px gap. The complete published card selects its template, including media, text and padding. Keep a native title button for Enter/Space access; the structural article must not become a nested button around Edit or media. Edit alone opens authoring and stops selection propagation. Card focus/selection uses one configured accent perimeter, including keyboard media preview focus. There is no management-mode switch.

Authors explicitly choose an image/video gallery cover using the shared file and
Template library picker, with no cover-mode dropdown or automatic fixed-reference
choice. Existing `cover_id` bindings and media remain unchanged. Selecting a shared
file reuses its identity; it does not upload or copy it. A draft can be saved without
a cover; publication requires a selected cover. Changing a fixed input does not
change the cover.

Cover media preserves its complete natural aspect ratio, including portrait
video, and has a textual failure state. Mouse hover previews pause on leave;
keyboard users focus the video and press Space/Enter; touch users tap the video. Cards have no separate Play/Pause preview button. Reduced-motion preferences
disable hover playback. Devices without hover ignore compatibility mouse-enter events after touch. Changing or leaving media disposes the previous player. Fixed image/video references use one full-width row of equal-width cells below the description. Preserve each natural aspect ratio with automatic height, without square cropping or stretching. Videos play muted, inline and loop automatically; they have no click action, controls or preview modal. Stop and dispose media when switching templates or leaving the runner.
The runner omits the prompt; Advanced editing opens the existing Playground after
explicit draft replacement confirmation.
The runner summary lists ratio, resolution and duration, then **Overridden Audio**
when an override asset is selected. Otherwise show the actual **Audio On** or
**Audio Off** setting. Omit external slot headings. Empty image blocks say
**Drop here an image of the {LABEL}**, substituting the configured label as safe
text. A selected block retains its slot identity inside the block. Preserve type
and required guidance in accessible descriptions and validation. There is no Clear
selection action; choosing another gallery card changes the template.
Missing slots use labeled file/library controls; fixed slots have no user editor.
Authoring reuses the shared native-textarea reference mirror and declared prompt
syntax contract. Reviewed variable slots count as bound references even without
fixed files; new unmatched mentions ask the author to infer/review slots. Draft
saving does not acquire generation-time file requirements. Caret, selection, IME,
scrolling and plain-text submission remain native.

Authoring file controls reuse the Playground reference dropzone/card patterns,
hide native file-input chrome, and provide keyboard picker buttons, drag/drop,
filename/preview, explicit assigned state, pending/cancel/error/retry feedback.
File choice alone does not establish a binding; only a completed, validated API upload or explicit shared-file selection does.
Template page headings use the console page-size token, and actions use the body
font with 44px targets and 12px gaps. These Templates rules live in templates.css;
they do not change global headings or unrelated Settings styles.
The Administrator uses the shared versioned Save pattern. All new surfaces use
shared theme tokens and stack within the 320px viewport. No visual exception is
required.

## Shared generation key panel

Playground and Templates move the same key panel between their hosts. Keep the
credential field, Forget key and a secondary Manage keys button, followed by
the key name and current weekly usage / total limit from the existing key budget response. Unknown usage or bounds remain unavailable, zero remains zero, and a null total is Unlimited. Omit saved-state actions, policy/billing prose,
reset dates and storage usage. Show creation/restoration actions only without an
active key. Preserve transient loading/error feedback, keyboard focus after
Forget and existing storage/generation enforcement outside this panel. Actions
wrap within narrow panels using shared button styles; no design exception.

Override Sound uses the shared `ReferenceUpload` dropzone, keyboard file picker,
progress/cancel/retry feedback and native audio preview in Playground and the
Template editor. A selected sound shows only the concise Remove action inside
its selection block; removal reveals the existing audio upload control. Uploaded
library media retains its existing lifecycle. It adds no independent library
picker. The section is available for video output and remains visible when an
existing template assignment needs removal after a model change.

## Shared interaction perimeter and alignment

Fields, buttons, switches and selectable Template/reference cards use one visible border. Focus or selection changes that existing perimeter to the configured accent and increases its thickness; never add a second outline, offset ring, separated contour or box-shadow imitation of a border. The shared styles reserve the original layout border and paint the single actual CSS border on its owner, keeping field labels, content and outer geometry stable. Scrollable owners and padded choice/drop blocks may change their native border with equal padding compensation. Borderless controls retain a flush keyboard-focus indicator without an offset. Selected cards use 2px; selected cards with keyboard focus use 3px on the same edge. Child actions keep their own focus state without making the parent appear selected. Forced-colors mode uses system Highlight/CanvasText on that perimeter. Error, disabled and combined states must remain distinguishable.

Primary buttons center their content vertically through the shared flex alignment, including text, icons, pending and disabled states. Do not fix individual labels with transforms or per-screen offsets.

Prompt labels use the common FormUI code-point counter and declared model maximum: **Prompt (10,000 characters max)** when empty and **Prompt (5,550 / 10,000 characters)** when filled. Remove detached duplicate counts, preserve validation and update on typing, paste, clear and model changes. Unknown model bounds remain unknown.

Use Image URLs and Advanced Settings disclosures match Image References heading typography, with the shared chevron after the text: right when collapsed, up when expanded, and 16px before expanded content. Ordinary dropdown arrows retain their existing direction. Keep native keyboard and expanded semantics, retain values through collapse and omit the separator before Advanced Settings. Override Sound has its own sibling panel and heading after Output settings.

Templates have one current content and a Published visibility switch. Save applies current edits immediately, including to published templates. Do not expose internal revisions, a separate published draft or version history in the interface. Compare every UI change with this design system and complete the spacing and visual review gate above.
