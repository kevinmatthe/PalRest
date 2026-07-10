# Player Activity Analytics Design

## Goal

Extend the existing WebUI with analytics for every player observed through the Palworld REST API. The feature shows current online state, today and weekly playtime rankings, server-wide activity for the last 7 or 30 days, and per-player activity for the same ranges.

Analytics are independent of playtime policy. Disabled, exempt, and unrestricted players are included whenever a successful poll observes them online.

## Scope

The first release includes:

- current online count and player online state;
- today's total observed playtime, today's peak concurrency, and today's active-player count;
- today and current-week player rankings;
- 7-day and 30-day server concurrency charts;
- 7-day and 30-day daily playtime charts for a selected player;
- 90-day rolling retention for analytics data;
- responsive desktop and mobile presentation;
- smooth chart transitions between WebUI refreshes.

It does not include arbitrary date ranges, session administration, data export, or retrospective reconstruction from existing aggregate policy usage.

## Architecture

An analytics recorder consumes the same successful online-player observation used by the guard service. It remains a separate unit from policy accounting so policy configuration cannot change analytics eligibility or totals.

The poll flow is:

1. The Palworld client returns the current online-player list.
2. The analytics recorder compares it with the previous successful observation.
3. The recorder opens or closes player sessions and apportions observed time into daily player totals and 5-minute server concurrency buckets.
4. The guard service independently performs policy accounting and enforcement.
5. Read-only analytics APIs query pre-aggregated data for the WebUI.

The recorder and repository expose narrow interfaces so session tracking, aggregation, querying, and retention cleanup can be tested independently.

## Observation Semantics

Only successful polls change analytics state. A failed poll does not imply that any player went offline and does not create a zero-concurrency bucket.

Observed time is accepted only when the gap between successful polls is within the configured `server.max_observation_gap`. This matches the guard's conservative accounting boundary. When the gap is larger, the unknown interval is excluded rather than assigned to online or offline time. A later successful poll establishes the next known state.

Current online values are returned with an `as_of` timestamp identifying the latest successful poll. Clients can therefore distinguish a known zero from stale data.

Time is stored in UTC. Daily and weekly reporting boundaries use the policy document's configured timezone. A session crossing a local midnight is split across the corresponding daily aggregates. The current week starts on Monday in that timezone.

## Storage Model

### `player_sessions`

Stores observed online sessions:

- player user ID;
- start time;
- nullable end time for a currently open session;
- last observed time;
- close reason.

There is at most one open session per player. Sessions preserve accurate last-online and session boundaries without requiring raw poll snapshots.

### `concurrency_buckets`

Stores 5-minute UTC buckets:

- bucket start;
- time-weighted average online-player count;
- maximum observed online-player count and the first time that maximum occurred;
- observed duration and coverage ratio.

Each accepted interval contributes its known duration and count to the bucket, splitting at bucket boundaries when necessary. The chart uses the time-weighted average, while today's peak metric uses the maximum observation. Coverage makes partial and missing buckets explicit. A bucket with no accepted observation is not equivalent to zero players.

### `player_daily_stats`

Stores per-player aggregates keyed by user ID and local calendar date:

- observed online duration;
- first observed time;
- last observed time;
- session count.

This table supports rankings and personal charts without scanning all sessions.

All analytics tables receive supporting time and player indexes. Existing `usage_periods` and `policy_states` remain unchanged because their meanings depend on policy strategy and are unsuitable as general activity history.

## Retention

Analytics data use a 90-day rolling retention window. A low-frequency cleanup job deletes expired closed sessions, concurrency buckets, and player daily statistics in bounded batches. It never deletes the currently open session for an online player.

Cleanup failure is logged and retried later. It does not block polling, analytics recording, or enforcement. Policy usage data retain their existing lifecycle.

## API

### Summary

`GET /api/v1/analytics/summary?ranking=today|week`

Returns:

- latest known online count and `as_of` time;
- today's total observed playtime;
- today's peak concurrency and peak time;
- today's distinct active-player count;
- the selected ranking period;
- a stable ordered player ranking with observed duration and online state.

Ranking defaults to `today`. Rankings sort by observed duration descending, then display name ascending, then user ID ascending. Invalid query values return HTTP 400 with the existing structured error format.

### Activity

`GET /api/v1/analytics/activity?range=7d|30d&user_id=<optional>`

Returns:

- range and reporting timezone;
- ordered server concurrency points with bucket time, time-weighted average count, maximum count, and coverage;
- when `user_id` is supplied, ordered daily playtime points for that player.

Range defaults to `7d`. Unsupported ranges and unknown players return HTTP 400 and 404 respectively. A concurrency bucket is chartable when it contains at least one accepted observation interval; its coverage is returned so partial buckets can be identified. Buckets with no accepted interval use `null` values so the chart draws a gap rather than a false zero.

Both endpoints are read-only and follow the current dashboard's unauthenticated read access.

## WebUI

The top bar adds an `Overview / Analytics` view switch. Overview retains the existing operational dashboard. Analytics contains:

1. Four metric cards for online now, today's total playtime, today's peak concurrency, and today's active players.
2. A server concurrency chart with 7-day and 30-day controls.
3. A compact ranking panel with today and current-week controls.
4. A searchable player selector and that player's daily playtime chart for the selected range.

On wide screens, the server chart and ranking share a two-column row. On narrow screens, all content becomes a single column; metric cards use two columns where space permits and then one column. Interactive controls provide at least a 44-pixel touch target.

The WebUI keeps its 10-second refresh cadence. When new points arrive, the SVG path and time window transition over approximately 400–600 milliseconds, producing a smooth leftward movement instead of a jump. Animation affects presentation only: missing observations remain visible gaps and are never visually interpolated as real measurements. `prefers-reduced-motion: reduce` disables transitions.

The first version uses lightweight React and SVG components instead of adding a large charting dependency.

## Error Handling

- Analytics recording errors are returned from the observation transaction and logged with context. They must not silently create partial session and aggregate updates.
- Poll failures preserve the latest known online state but expose its age through `as_of`.
- Gaps larger than `max_observation_gap` contribute no duration and produce missing chart regions.
- Analytics API failures use the existing dashboard error notice while retaining previously loaded data.
- Cleanup errors are non-fatal and retried on the next scheduled cleanup.
- A player with no activity in the selected range receives an explicit empty series rather than fabricated zero history before the player was first seen.

## Testing

Backend tests cover:

- first observation and session opening;
- normal continuation and logout;
- players joining and leaving in one poll;
- failed polls and recovery within or beyond `max_observation_gap`;
- local-midnight and timezone-boundary splitting;
- 5-minute bucket boundaries and partial coverage;
- today and Monday-based week rankings, including stable tie ordering;
- 7-day and 30-day API range validation;
- missing buckets and unknown-player responses;
- 90-day cleanup without deleting open sessions;
- transaction rollback when any analytics write fails.

Frontend tests cover:

- metric, ranking, range, and player-selection rendering;
- loading, empty, stale, missing-point, and API error states;
- SVG gaps for null data;
- smooth updates when new samples arrive;
- reduced-motion behavior;
- desktop and narrow-screen layout classes and accessible controls.

## Migration and Compatibility

The feature adds new SQLite tables through the next schema migration and does not reinterpret existing rows. Historical charts begin accumulating after deployment; the UI clearly indicates when fewer than 7 or 30 days are available.

Existing player, policy, health, status, and management API contracts remain compatible. Analytics endpoints are additive.

## Success Criteria

- Every player returned by successful Palworld polls contributes to analytics regardless of policy state.
- Current online status reflects the latest successful observation and includes its timestamp.
- Rankings and charts use only conservatively observed time and do not inflate totals across poll failures.
- The WebUI can switch between 7-day and 30-day views and today/week rankings without page reloads.
- Chart updates move smoothly while respecting reduced-motion settings.
- Analytics older than 90 days are removed without affecting open sessions or policy usage.
