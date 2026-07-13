# Palworld Trace, World, and Analytics Design

## Goal

Extend PlaytimeGuard into a Palworld activity and world-observation system without weakening its conservative playtime enforcement. The system combines player sessions, Guard actions, the full read-only Palworld REST surface, map-based movement replay, and read-only save parsing into one auditable timeline.

SQLite remains the authority for business facts and long-term aggregates. A separate save worker isolates expensive parsing. The existing Victoria stack is an optional observability enhancement and is never required for Guard decisions or product queries.

## Scope

The feature includes:

- a unified player and server event timeline;
- sampled exact player positions and map replay with a shared time cursor;
- REST server metrics, server information, settings versions, and the complete player fields;
- player, guild, and base-camp data parsed from stable save snapshots;
- player, server, and guild replay modes;
- continuous-play, activity-pattern, retention, regional-presence, and long-term aggregate analytics;
- public aggregate views and administrator-only sensitive views;
- optional OTLP metrics, technical traces, and structured event mirrors;
- tiered retention: 90-day raw data and long-lived daily, weekly, and monthly aggregates.

Detailed inventories, equipment, captured-Pal collections, and write access to Palworld saves are excluded. RCON and container infrastructure metrics may be added later but are not part of this design.

## Design Principles

1. Enforcement remains conservative. Only a successful `/players` observation and a successful SQLite transaction can advance online accounting or create online-state facts.
2. Optional components cannot block the Guard. Save parsing, Victoria, map assets, and advanced analytics may fail independently.
3. Sources retain their semantics. REST observations, Guard actions, save facts, and derived analytics are not presented as equally timely or equally certain.
4. Sensitive data are explicit. IP addresses, exact positions, and personal replay require administrator access and generate access-audit records.
5. Unknown intervals remain unknown. The system never draws movement across observation gaps or fills missing metrics with zero.

## Architecture

### Guard Main Service

The existing Go service continues to own:

- Palworld REST polling;
- playtime policy evaluation, warnings, and enforcement;
- normalization of REST observations;
- the business event journal, sessions, trajectories, and aggregates;
- SQLite migrations and transactions;
- public and administrator APIs;
- asynchronous OTLP export.

The `/players` request is the critical fast path. REST metrics, server information, and settings are separate requests with independent success states so their failure cannot invalidate a successful player observation.

### Save Worker

An optional worker performs only the resource-intensive save path:

1. discover a candidate Palworld backup or active save;
2. verify stability;
3. copy the required files into a private snapshot directory;
4. calculate a content fingerprint;
5. parse the copy under CPU, memory, timeout, and single-concurrency limits;
6. return a versioned normalized result to the main service;
7. let the main service validate and atomically import the result.

The worker never changes original save files. Its crash, timeout, or out-of-memory termination affects only the current snapshot job. Deployments without a save mount or worker still provide Guard, REST analytics, and online replay.

The normalized contract covers players, identity hints, guild membership, guild administration, base camps, base-camp areas, source timestamps, parser version, and source fingerprint. It intentionally excludes detailed inventories and Pals.

### SQLite

SQLite is the product authority for:

- online sessions and business events;
- Guard action attempts and results;
- exact trajectory samples;
- current player profiles and identity mappings;
- server information and settings versions;
- save job history and snapshot provenance;
- versioned guild and base-camp state;
- daily, weekly, and monthly aggregates;
- sensitive-data access auditing.

Existing Analytics tables and API contracts remain compatible. Existing `player_sessions` rows participate in the unified timeline without historical rewriting.

### Victoria and OpenTelemetry

The existing OTLP collector and Victoria services are optional enhancements:

- VictoriaMetrics stores high-frequency numeric series such as server FPS, frame time, online count, capacity, uptime, game day, base-camp count, player ping distributions, snapshot size, and parser duration.
- VictoriaTraces stores technical spans for REST polling, SQLite transactions, snapshot copying, parsing, importing, and API requests.
- VictoriaLogs may receive a structured mirror of non-secret business events for operations-wide search.

Player activity is a business timeline, not a distributed trace. VictoriaTraces is not used as its authority. OTLP uses a bounded asynchronous queue and may drop exported copies during an outage. IP addresses, exact coordinates, credentials, and other sensitive values are prohibited from OTLP attributes, metric labels, and log bodies.

## REST Collection

### Players

`/players` runs at the configured poll interval, currently 30 seconds by default. The normalized observation includes user ID, player ID, display names, IP address, ping, exact coordinates, level, and building count when present.

A failed request:

- does not advance Guard usage;
- does not imply that anyone logged out;
- does not add trajectory points;
- does not create a zero-concurrency interval;
- preserves the previous state with a stale observation timestamp.

### Metrics

`/metrics` runs at the player polling cadence but commits independently. It captures server FPS, frame time, REST-reported player count, maximum player count, uptime, base-camp count when supported, and in-game day.

Metric failure produces an explicit gap. A discrepancy between `/metrics` player count and the normalized `/players` length is retained as a data-quality signal, not silently reconciled.

### Server Information and Settings

`/info` and `/settings` are read at startup and checked at a lower frequency. Canonical JSON is hashed. A new immutable version and change event are written only when the hash changes.

Settings include both gameplay balance and operational values. Passwords and unavailable secrets are never expected from or persisted through these endpoints.

## Save Snapshot Semantics

Palworld-managed backups are preferred over the active save. When backups are unavailable, the main service creates a read-only copy only after required files have unchanged size and modification time across a configured stability window. The snapshot includes `Level.sav` and the required `Players` files from one selected source directory.

The system does not claim filesystem-level atomicity across files. A parser or validation failure marks the candidate failed and leaves the current world view unchanged.

Save jobs progress through:

`discovered → copying → ready → parsing → imported`

Failure records contain the failed stage, safe diagnostic details, attempt count, and next retry time. A successful snapshot fingerprint is imported at most once. The default scheduler considers new stable data continuously but starts no more than one parse every ten minutes.

Every imported entity records the snapshot capture time, import time, source fingerprint, and parser schema/version. Reprocessing a snapshot with a newer parser creates a new import version while retaining provenance.

## Identity Model

REST `userId` remains the online and Guard identity. Save player UID remains the save identity. An explicit mapping table relates them and records mapping method, confidence, evidence, creation time, and supersession.

Only deterministic evidence may activate a mapping automatically. Ambiguous candidates remain separate pending administrator review. Correcting a mapping updates current projections through a new mapping version; it does not alter original source records.

## Unified Event Timeline

The event journal uses an immutable envelope containing:

- event ID and event type;
- subject type and subject ID;
- `occurred_at` and `observed_at`;
- source and source reference;
- correlation ID for one polling or enforcement cycle;
- confidence and schema version;
- a typed, validated payload.

Core events include player joined, player left, Guard warning attempted/delivered/failed, enforcement attempted/succeeded/failed, player attribute changed, server restarted, server version changed, server settings changed, save imported, player profile changed, guild membership changed, and base-camp state changed.

REST transitions use their observation time and `observed` confidence. Save-derived changes use the snapshot capture time when known, import time as `observed_at`, and snapshot-derived confidence. A late save event is placed at its source time but visibly marked as snapshot-derived.

OTel trace IDs may be stored as diagnostic correlation metadata but never serve as business event identifiers.

## Sessions and Trajectories

Sessions retain the current conservative observation rules. The first observation establishes a baseline. Intervals longer than `max_observation_gap` are excluded. A successful later observation starts a new known segment.

Each successful online player observation participates in trajectory sampling. A new exact sample is stored when any of the following holds:

- it is the first point in a known segment;
- distance from the last stored point exceeds a configurable movement threshold;
- the maximum sample interval has elapsed;
- level, ping state, or another replay-relevant attribute crosses a configured change boundary.

Small coordinate jitter while stationary is suppressed. Coordinates equal to an API-defined absent value are not treated as real map positions.

Trajectory segments break across failed observations, excessive gaps, server restarts, invalid coordinates, and identity uncertainty. The WebUI may interpolate movement between consecutive points inside one valid segment for animation only. It must not interpolate across a segment break.

## Real Map and Replay

The Replay page renders real Palworld map tiles using Leaflet-compatible coordinates. Map tiles, world bounds, coordinate transformation, fast-travel points, tower locations, and other static overlays are versioned assets. Asset licensing and provenance must be documented before distribution; reference-repository assets are not copied solely because their code is available.

Replay combines:

- past and future portions of a player's valid trajectory segment;
- current player marker and replay-relevant player attributes;
- base-camp markers and areas from the latest snapshot valid at the cursor time;
- optional static locations such as fast-travel points and towers;
- Guard and server events on the timeline;
- a server-metric summary at the cursor time.

A single cursor drives the map, timeline, event details, and metric panel. Users can drag, play, pause, and change speed. Replay supports player, whole-server, and guild scopes. Queries are bounded by time range and map bounding box; the server applies resolution-aware downsampling instead of returning an entire 90-day trajectory.

Player replay and exact location layers are administrator-only. Public pages never expose personal movement.

## Analytics

Existing current, daily, weekly, 7-day, and 30-day Analytics remain available. New derived views include:

- continuous-play streaks and rest intervals;
- activity by hour of day and day of week;
- player return frequency and cohort retention;
- guild activity and member overlap;
- server FPS and frame-time correlation with concurrency;
- region dwell time and daily movement distance;
- base-camp and guild changes over save snapshots;
- daily, weekly, and monthly long-term rollups.

Derived values preserve coverage. A statistic whose source range contains unknown intervals exposes coverage or incomplete status rather than pretending to be exact.

## Retention

Default retention is tiered:

- exact trajectories, raw business events, detailed REST metrics in SQLite, and closed session detail: 90 days;
- VictoriaMetrics: controlled by its existing stack policy, currently 180 days;
- VictoriaTraces and VictoriaLogs: controlled by their existing stack policy, currently 30 days;
- daily, weekly, and monthly product aggregates: retained indefinitely;
- save parsing results and entity versions: retained as long-lived world history;
- original snapshot copies: only a small configurable recent set, subject to available storage;
- save job metadata and fingerprints: retained long enough to preserve import provenance.

Cleanup is bounded and resumable. It never deletes open sessions, active jobs, current entity projections, audit records required by policy, or provenance still referenced by retained entity versions.

## API and Authorization

### Public APIs

Public read endpoints may return:

- current server status and data freshness;
- aggregate concurrency and performance metrics;
- anonymized or existing display-name rankings according to current product policy;
- aggregate activity patterns and retention;
- health and coverage indicators.

They never return IP addresses, exact coordinates, personal trajectories, raw event payloads, or detailed save-derived player records.

### Administrator APIs

Administrator authentication is required for:

- player sessions and detailed event timelines;
- exact trajectories and all Replay queries;
- IP address history and exact coordinates;
- player, guild, and base-camp snapshot detail;
- save jobs, parser diagnostics, and mapping review;
- server configuration history containing operational details.

Each sensitive read writes an access-audit row containing actor, request time, object type and identifier, requested time range, purpose/query category, and outcome. Response content and secrets are not copied into the audit record.

Map APIs require a bounded time range, scope, and optional bounding box. Responses contain ordered trajectory segments, business events, effective world snapshots, metric summaries, source timestamps, coverage, stale state, and pagination or resolution metadata.

## WebUI Information Architecture

- **Overview** retains Guard health, current online players, current usage, and enforcement status.
- **Analytics** adds activity patterns, continuous-play analysis, retention, guild statistics, and long-term trends to the existing dashboard.
- **Replay** contains the map, unified cursor, scope selector, event filters, layer controls, and time-synchronized details.
- **World** contains player profiles, guilds, base camps, snapshot differences, identity mapping status, and server settings history.
- **Policies** retains existing policy management.

Desktop Replay uses a large map with a side detail panel and a bottom timeline. Narrow screens prioritize the map and cursor, with filters and details in accessible drawers. Playback respects `prefers-reduced-motion`; reduced-motion mode keeps cursor stepping and data updates but disables animated marker movement.

## Failure Handling and Data Health

- `/players` failure freezes Guard accounting and creates an unknown observation interval.
- `/metrics`, `/info`, or `/settings` failure affects only that stream.
- SQLite failure prevents the relevant state transition and is surfaced through health status.
- save discovery, copy, parse, validation, or import failure leaves the last imported world view intact.
- worker resource exhaustion terminates the job and schedules bounded retry without affecting the main service.
- Victoria or collector failure increments local export-failure metrics and may drop optional exports without blocking business writes.
- map-asset failure leaves timeline and tabular event detail usable while showing an explicit map error.
- analytics and replay responses identify source timestamps, coverage, stale state, and gaps.

## Migration and Compatibility

Migrations are additive. They introduce the event journal, trajectories, REST metric samples, server-info and settings versions, snapshot jobs, snapshot imports, identity mappings, guild/base versions, long-term aggregates, and sensitive-access audit tables.

Existing policy, usage, sessions, Analytics, health, and management contracts remain compatible. Historical sessions are queryable in the unified timeline, but positions, REST metrics, and save facts begin only after deployment. No historical movement is synthesized.

## Delivery Phases

### Phase 1: REST and Unified Timeline

Add complete REST normalization, metrics/info/settings collection, business events, Guard action correlation, server metric storage, administrator timeline APIs, and access auditing foundations.

### Phase 2: Real Map Replay

Add licensed/versioned map assets, coordinate transforms, trajectory storage, segment and downsampling queries, synchronized playback, and player/server/guild Replay views.

### Phase 3: Save Worker and World Views

Add stable snapshot discovery and copying, isolated parsing, normalized imports, identity mapping, guild/base history, and the World UI.

### Phase 4: Victoria and Advanced Analytics

Add optional OTLP export, operational dashboards, continuous-play and activity-pattern views, retention, regional dwell, movement, performance correlation, and long-term rollups.

Each phase is independently deployable. Phase 1 must preserve all existing Guard behavior before later phases begin.

## Testing

Backend coverage includes:

- regression tests for all existing Guard, policy, and Analytics behavior;
- independent REST endpoint success, failure, staleness, and recovery;
- atomic event, session, trajectory, and aggregate writes;
- coordinate validation, jitter suppression, segment breaks, cross-midnight behavior, and out-of-order observations;
- server restart detection from uptime/version signals;
- settings canonicalization and change detection;
- snapshot stability, duplicate fingerprints, retries, parser timeout/resource failure, validation, and transaction rollback;
- deterministic and ambiguous REST/save identity mapping;
- guild and base-camp version reconstruction at a cursor time;
- public response redaction and administrator enforcement;
- mandatory audit records for sensitive queries;
- bounded retention without deleting active state or required provenance;
- Victoria outage behavior and bounded exporter queues.

Frontend coverage includes:

- map coordinate fixtures and real tile alignment;
- cursor synchronization among marker, trajectory, events, world state, and metrics;
- no interpolation across unknown segments;
- player, server, and guild replay scopes;
- layer filtering, bounding-box loading, downsampling, and pagination;
- public/admin authorization boundaries;
- loading, stale, missing-map, partial-source, and error states;
- responsive desktop/mobile behavior, keyboard control, touch targets, and reduced motion.

End-to-end fixtures contain sanitized REST responses and small legally redistributable save samples. Large-save resource tests run separately from the normal unit suite.

## Success Criteria

- Guard accounting and enforcement remain correct when Save Worker, Victoria, or map assets are unavailable.
- A successful player observation can be traced through its session, Guard actions, coordinates, and derived aggregates.
- Map replay uses real, versioned Palworld map data and never invents a path across unknown intervals.
- Save-derived player, guild, and base state is traceable to a stable snapshot fingerprint and parser version.
- Public APIs expose no IP address, exact coordinate, personal trajectory, or sensitive save detail.
- Every administrator sensitive-data read creates an access-audit record.
- Raw detail expires according to the 90-day policy while long-term aggregates remain queryable.
- Victoria improves observability but never becomes a prerequisite for product correctness.
