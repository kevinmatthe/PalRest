# Policy Management and Standard Login Design

## Goal

Replace the compact top-bar credential controls and raw JSON Policy editor with a password-manager-compatible login modal and a complete visual Policy management interface. Make SQLite the sole runtime Policy source after initialization, with code defaults as the final fallback and `conf.yaml` serving only as an optional bootstrap default.

## Scope

This change covers:

- A standard administrator login modal while preserving unauthenticated read-only access.
- A dedicated visual Policy manager for the global default and per-player overrides.
- Code-owned Policy fallback values and partial YAML bootstrap defaults.
- Database-authoritative runtime Policy behavior.
- Existing Policy document compatibility, validation, automated tests, and responsive behavior.

It does not add passkeys, multiple administrator accounts, Policy history, audit-log UI, or role-based permissions.

## Authentication Interaction

The read-only dashboard remains visible without authentication. The top bar shows a single administrator login button instead of inline username and password inputs. Activating it opens an accessible modal containing a real HTML form with:

- A username input named `username` with `autocomplete="username"`.
- A password input named `password` with `autocomplete="current-password"`.
- Explicit labels, a submit button, initial focus, Escape-to-close behavior, and focus restoration.
- An inline authentication error that does not remove the entered username.

The password is cleared when the modal closes or login succeeds. Credentials are never stored in browser storage. Successful login closes the modal and refreshes the administrator session; logout immediately disables write actions while preserving read-only dashboard data.

On narrow screens the dialog uses nearly the full viewport width. Form controls and dialog actions have touch targets of at least 44 pixels.

## Policy Source and Initialization

The effective precedence is:

1. A Policy document already stored in SQLite.
2. On an empty database only, `conf.yaml` Policy bootstrap values overlaid on code defaults.
3. Code defaults for every field absent from both the database and YAML bootstrap configuration.

Once a Policy document exists in SQLite, it is the only runtime Policy source. Later edits or hot reloads of the YAML Policy section have no effect and never overwrite the stored document.

The YAML `policy` section is optional and may partially specify only `timezone` and `default`. YAML player overrides are no longer supported; all player overrides are created through the management API and stored in SQLite. Unknown Policy fields remain configuration errors so obsolete `policy.overrides` entries are not silently ignored.

On the first startup with no stored Policy, the application resolves code defaults plus YAML bootstrap values, validates the complete result, and persists it before the Policy service becomes available. From that point onward its source is reported as `database`.

Existing valid rows in `policy_documents` continue to load without a destructive migration. An existing database Policy is never replaced by new defaults.

## Code Defaults

Code contains a complete, safe Policy:

- Disabled by default.
- Timezone `Asia/Shanghai`.
- Fixed-window strategy.
- Daily period resetting at `04:00`.
- Two-hour limit.
- Warning thresholds at 30, 10, 5, and 1 minute.
- No player overrides.

Strategy-specific fallback values are also complete so switching strategy in the visual editor begins with valid inputs:

- Cooldown: two hours of play followed by 30 minutes of rest.
- Credit: recover 30 minutes every hour, capped at three hours.

These values are initialization defaults, not a second live configuration source.

## Persistence and Runtime Updates

The existing singleton Policy document remains the canonical database representation. This keeps current installations compatible and avoids unnecessary table normalization. The management API reads and writes typed JSON DTOs while the service persists the validated document.

A save follows this order:

1. Decode the complete typed Policy payload.
2. Validate all global and override rules.
3. Persist the document to SQLite.
4. Replace the in-memory Policy and timezone under the service lock.
5. Return the canonical saved Policy with `source: "database"`.

Validation or persistence failure leaves the previous in-memory Policy active. The client retains its unsaved form values and displays the server error.

## Policy Management Interface

Policy management moves out of the dashboard side panel into a dedicated management view available to authenticated administrators. Its desktop layout is a master-detail interface:

- The left pane contains `Global default`, a searchable list of player overrides, and an add button.
- The right pane contains the selected rule's typed editor.
- Known players display their current name with the stable User ID underneath.
- An override may be created by selecting a known player from database-backed player results or by manually entering a User ID.

On narrow screens, the master list and editor become consecutive views with a visible back action rather than compressed columns.

Automatic dashboard refreshes do not overwrite an open Policy draft. Changing the selected rule with unsaved edits requires the administrator to save or discard the draft.

## Global Default Editor

The global editor provides:

- Policy enabled switch.
- IANA timezone input.
- Strategy selector with fixed-window, cooldown, and credit options.
- Daily or weekly period selector.
- Reset time, plus reset weekday when the period is weekly.
- Fixed-window limit.
- Cooldown play duration and required rest duration.
- Credit recovery interval, recovery amount, and maximum credit.
- Addable and removable warning thresholds.

Only fields relevant to the selected strategy are shown. Duration controls use a positive numeric value and a minute/hour unit selector. The frontend converts these values to integer milliseconds for the API; administrators never need to edit duration syntax or JSON.

## Player Override Editor

The player override editor identifies the player by display name and stable User ID and provides an exempt switch. Each configurable group supports inheritance from the global default:

- Enabled state has explicit `inherit`, `enabled`, and `disabled` states.
- Strategy, schedule, strategy-specific durations, and warnings each support `inherit` or `custom`.
- Inherited values remain visible as read-only context where useful but are omitted from the override payload.
- Custom fields use the same conditional controls and validation rules as the global editor.

Deleting an override requires confirmation. Duplicate or blank manual User IDs are rejected. Exemption disables enforcement but preserves other saved override fields so removing the exemption restores the configured rule.

## Validation and Errors

The backend is authoritative for validation. It rejects:

- Unknown strategies or periods.
- Invalid IANA timezones, reset clocks, or weekly reset weekdays.
- Non-positive strategy durations and limits.
- Empty player IDs.
- Duplicate, non-positive, or ineffective warning thresholds.
- Warning thresholds that are not below the applicable strategy allowance.

The frontend performs matching immediate checks for usability but does not replace server validation. Errors appear next to the relevant field when possible; unexpected request errors appear in a form-level notice. Saving disables duplicate submission and a successful save uses the server response as the new local baseline.

## API Contract

The existing session, login, logout, player, and Policy routes remain. `GET /api/v1/policies` and successful `PUT /api/v1/policies` add:

```json
{
  "version": 1,
  "source": "database",
  "timezone": "Asia/Shanghai",
  "default": {},
  "overrides": {}
}
```

The Policy payload remains a complete document on save. Known players already come from `GET /api/v1/players`; the UI maps override User IDs to those results and supports IDs not present in the player list.

## Testing

Backend tests cover:

- Complete code defaults with an omitted YAML Policy.
- Partial YAML defaults overlaid on code defaults.
- Rejection of YAML player overrides.
- Initial database seeding and source reporting.
- Existing database precedence over YAML at startup.
- YAML hot reload having no effect on the active database Policy.
- Existing Policy document compatibility.
- Atomic behavior when validation or persistence fails.
- Strategy and override validation through the API.

Frontend tests cover:

- Standard form names and autocomplete attributes used by password managers.
- Login success, authentication errors, modal closure, and password clearing.
- Conditional strategy and weekly-period fields.
- Duration-unit conversion.
- Override inheritance and exemption payloads.
- Known-player selection, manual User ID entry, duplicate rejection, deletion confirmation, and unsaved-change protection.
- Master-detail desktop behavior and narrow-screen navigation.

Final verification runs all Go tests, frontend tests, the TypeScript/Vite production build, and browser checks at desktop and mobile viewport sizes.

## Acceptance Criteria

1. Common password managers recognize and fill the administrator credentials in a standard modal form.
2. Unauthenticated users retain read-only dashboard access; authenticated users can enter Policy management and perform writes.
3. Administrators can edit every supported Policy option without writing JSON or duration syntax.
4. Administrators can create, edit, exempt, and delete player overrides using either known-player search or manual User IDs.
5. An existing database Policy is authoritative and unaffected by YAML changes or hot reloads.
6. An empty database is initialized once from partial YAML defaults over complete code defaults.
7. YAML cannot define player overrides.
8. Existing stored Policy documents continue to work.
9. Invalid or failed saves do not replace the active Policy or erase the editor draft.
10. Automated backend and frontend tests pass, the production frontend builds, and the interface works at desktop and mobile widths.
