# Overlay team map and performance implementation plan

**Goal:** Show all positioned online players without opening the game's map, while reducing resident Windows HUD overhead.

**Architecture:** A shortcut/tray opens an opaque, resizable, always-on-top team-map window on demand. It uses the existing live positions endpoint and private tiles. Closing destroys the window. The HUD keeps its existing geometry and click-through behavior. No trajectory interpolation or animation loop is introduced.

**Tech stack:** Existing Tauri 2 / Rust, React, Leaflet; native global shortcut plugin.

- [x] Native: restricted live-position command, on-demand window lifecycle, shortcut and tray fallback, visibility signals, minimal Windows process refresh. Test HTTP policy and lifecycle contracts.
- [x] Data: validated coordinates, serialized five-second polling, no regression to older snapshots, stale/failed last-known state, abort and stop on hidden/unmounted windows. Test races and cleanup.
- [x] UI: all-player fit, self-follow, manual pan/zoom, safe names and overlapping player groups, source age and empty/error states. Reuse markers and disable tile/zoom animations. Test rendering and lifetime.
- [x] HUD: remove backdrop blur and whole-layer filters, disable minimap animation, avoid callback-triggered map recreation, align successful polling with five-second server cadence, suspend hidden HUD.
- [x] Integrate: full overlay JS tests/build and Rust tests/checks; browser smoke check if available. Document keyboard access and Windows FPS comparison procedure. Do not claim measured FPS gains without a Windows game run.

Existing unrelated Docker changes are outside scope. User confirmed Windows; exact FPS and hidden-state recovery remain unknown.

Validation: 315 overlay frontend tests and 82 Rust no-default-features tests pass; production frontend build succeeds. Chromium smoke at 640×560 and 400×360 confirms grouping, viewport controls, Escape, no overflow/page errors, and five-second request cadence. Native build is blocked in this Linux environment by missing GTK packages and Windows cross-compilers; Windows game FPS comparison remains an explicit target-platform validation item.

Follow-up: all-player map now infers possible fast travel only when adjacent observations are at distinct fast-travel POIs within 15 seconds, within 25,000 raw units at each end, and move at least 50,000 units. Static dashed arrows connect POI centers, expire after approximately 30 seconds, and are capped at 20. Detection resets across failure, timeout, visibility pause and out-of-order responses. No per-frame animation or extra polling.
