# Grafana dashboard: Palrest

File: `palrest-dashboard.json`

## Import

1. Grafana → **Dashboards** → **New** → **Import**
2. Upload `palrest-dashboard.json`
3. When prompted for **VictoriaMetrics / Prometheus**, pick the PromQL datasource
   that scrapes `http://palworld-playtime-guard:8080/metrics`
4. Import

## If panels show wrong / empty datasource

- Re-import the JSON (do not only overwrite panels) and select the datasource again.
- The dashboard expects a **Prometheus-compatible** datasource type (`prometheus`).
  - VictoriaMetrics: create a Prometheus datasource whose URL is Victoria select
    (e.g. `http://victoriametrics:8428` or your vmsingle/vmselect path).
  - Do **not** leave `${DS_PROMETHEUS}` unresolved.
- Explore: run `palrest_up` in that datasource; if empty, scrape is wrong, not the panel.

## XY scatter panel

Uses `palrest_player_location_x` / `_y` (game world units). Needs online players with valid coords.
