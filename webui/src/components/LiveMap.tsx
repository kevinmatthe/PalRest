import { useEffect, useMemo, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { Map as MapIcon, Radio, RefreshCw, Users } from 'lucide-react';
import { getLivePositions, type LivePositionPlayer, type LivePositionsResponse } from '../api';
import {
  formatTimelineDateTime,
  PALWORLD_TILE_BOUNDS,
  PALWORLD_TILE_FALLBACK_URL,
  PALWORLD_TILE_URL,
  projectWorldXY,
} from './timelineShared';
import { tileErrorTransition } from './TimelineMap';

export type LiveMapProps = {
  /** Bump to force refresh (e.g. global refresh button). */
  refreshKey?: number;
  /** Open player timeline for this user. */
  onOpenPlayer?: (userID: string) => void;
};

const FallbackTileLayer = L.TileLayer.extend({
  createTile(this: L.TileLayer, coords: L.Coords, done: L.DoneCallback) {
    const tile = document.createElement('img');
    tile.setAttribute('role', 'presentation');
    tile.alt = '';
    L.DomEvent.on(tile, 'load', L.Util.bind((this as unknown as { _tileOnLoad: (cb: L.DoneCallback, t: HTMLImageElement) => void })._tileOnLoad, this, done, tile));
    tile.onerror = () => {
      const transition = tileErrorTransition(tile.dataset.fellBack === 'true', { x: coords.x, y: coords.y, z: this._getZoomForUrl() });
      if (transition.action === 'fail') {
        (this as unknown as { _tileOnError: (cb: L.DoneCallback, t: HTMLImageElement, e: Error) => void })._tileOnError(done, tile, new Error('tile unavailable'));
        return;
      }
      tile.dataset.fellBack = 'true';
      tile.src = transition.src;
    };
    if (this.options.crossOrigin || this.options.crossOrigin === '') {
      tile.crossOrigin = this.options.crossOrigin === true ? '' : this.options.crossOrigin;
    }
    tile.src = this.getTileUrl(coords);
    return tile;
  },
}) as unknown as new (url: string, options?: L.TileLayerOptions) => L.TileLayer;

const POLL_MS = 5_000;

export function LiveMap({ refreshKey = 0, onOpenPlayer }: LiveMapProps) {
  const mapElementRef = useRef<HTMLDivElement>(null);
  const leafletRef = useRef<L.Map | null>(null);
  const markersLayerRef = useRef<L.LayerGroup | null>(null);
  const onOpenPlayerRef = useRef(onOpenPlayer);
  onOpenPlayerRef.current = onOpenPlayer;

  const [mapAvailable, setMapAvailable] = useState(true);
  const [data, setData] = useState<LivePositionsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (!mapElementRef.current || leafletRef.current) return;
    const map = L.map(mapElementRef.current, {
      attributionControl: false,
      crs: L.CRS.Simple,
      center: [-128, 128],
      maxBounds: PALWORLD_TILE_BOUNDS,
      maxBoundsViscosity: 0.8,
      minZoom: 0,
      maxZoom: 6,
      zoom: 2,
      zoomControl: true,
    });
    const tileLayer = new FallbackTileLayer(PALWORLD_TILE_URL, {
      bounds: PALWORLD_TILE_BOUNDS,
      maxNativeZoom: 6,
      minNativeZoom: 0,
      noWrap: true,
      tileSize: 256,
    });
    tileLayer.on('tileerror', () => setMapAvailable(false));
    tileLayer.addTo(map);
    map.fitBounds(PALWORLD_TILE_BOUNDS);
    const markers = L.layerGroup().addTo(map);
    leafletRef.current = map;
    markersLayerRef.current = markers;
    const ro = typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(() => {
        try {
          map.invalidateSize({ animate: false });
        } catch {
          /* ignore */
        }
      })
      : null;
    ro?.observe(mapElementRef.current);
    requestAnimationFrame(() => {
      try {
        map.invalidateSize({ animate: false });
      } catch {
        /* ignore */
      }
    });
    return () => {
      ro?.disconnect();
      map.remove();
      leafletRef.current = null;
      markersLayerRef.current = null;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    setLoading(true);
    void getLivePositions(controller.signal)
      .then((payload) => {
        if (cancelled) return;
        setData(payload);
        setError(null);
      })
      .catch((err: unknown) => {
        if (cancelled || controller.signal.aborted) return;
        setError(err instanceof Error ? err.message : '无法加载实时位置');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [refreshKey, tick]);

  useEffect(() => {
    const id = window.setInterval(() => setTick((n) => n + 1), POLL_MS);
    return () => window.clearInterval(id);
  }, []);

  const players = data?.players ?? [];
  const selected = useMemo(
    () => players.find((p) => p.user_id === selectedID) ?? null,
    [players, selectedID],
  );

  useEffect(() => {
    const map = leafletRef.current;
    const layer = markersLayerRef.current;
    if (!map || !layer) return;
    layer.clearLayers();
    players.forEach((player) => {
      const active = player.user_id === selectedID;
      const latLng = projectWorldXY(player.x, player.y);
      const marker = L.circleMarker(latLng, {
        radius: active ? 10 : 7,
        color: active ? '#8d5a0f' : '#0f7285',
        fillColor: active ? '#f5c451' : '#3ec6d8',
        fillOpacity: 0.95,
        weight: active ? 3 : 2,
        className: `live-map-marker${active ? ' is-active' : ''}`,
      });
      const label = player.name || player.account_name || player.user_id;
      marker.bindTooltip(label, { direction: 'top', opacity: 0.95, permanent: active });
      marker.on('click', () => {
        setSelectedID(player.user_id);
        try {
          map.setView(latLng, Math.max(map.getZoom(), 3), { animate: true });
        } catch {
          /* ignore */
        }
      });
      marker.addTo(layer);
    });
  }, [players, selectedID]);

  function openSelected(player: LivePositionPlayer) {
    onOpenPlayerRef.current?.(player.user_id);
  }

  return (
    <section className="live-map-page" aria-label="实时地图">
      <header className="live-map-header">
        <div>
          <p className="eyebrow">全服位置</p>
          <h2>实时地图</h2>
          <p className="live-map-note">跟随 Guard 轮询刷新 · 约每 {POLL_MS / 1000}s · 仅在线且有坐标的玩家</p>
        </div>
        <div className="live-map-header-meta">
          <span className="live-map-stat">
            <Users size={15} />
            {data ? `${data.positioned}/${data.online_count} 有坐标` : '—'}
          </span>
          <span className="live-map-stat">
            <Radio size={15} />
            {data?.as_of ? formatTimelineDateTime(data.as_of) : loading ? '加载中…' : '尚观测'}
          </span>
          <button
            type="button"
            className="live-map-refresh"
            onClick={() => setTick((n) => n + 1)}
            disabled={loading}
          >
            <RefreshCw size={15} />
            刷新
          </button>
        </div>
      </header>

      {error ? (
        <div className="timeline-alert" role="alert">
          {error}
        </div>
      ) : null}

      <div className="live-map-stage">
        <div className="live-map-surface">
          <div
            aria-label="Palworld 实时玩家地图"
            className="live-map-leaflet"
            data-testid="live-map"
            ref={mapElementRef}
            role="img"
          />
          {!mapAvailable ? (
            <div className="timeline-map-missing" role="status">
              地图瓦片加载失败：请检查本地 <code>webui/public/map/tiles</code> 或网络。
            </div>
          ) : null}
          {!loading && players.length === 0 ? (
            <div className="live-map-empty">当前没有可显示坐标的在线玩家。</div>
          ) : null}
        </div>

        <aside className="live-map-roster" aria-label="在线玩家列表">
          <h3>
            <MapIcon size={16} />
            在线位置
          </h3>
          {loading && !data ? <p className="live-map-roster-empty">加载中…</p> : null}
          {!loading && players.length === 0 ? (
            <p className="live-map-roster-empty">暂无坐标点</p>
          ) : null}
          <ul className="live-map-roster-list">
            {players.map((player) => {
              const active = player.user_id === selectedID;
              const label = player.name || player.account_name || player.user_id;
              return (
                <li key={player.user_id}>
                  <button
                    type="button"
                    className={`live-map-roster-item${active ? ' is-active' : ''}`}
                    aria-pressed={active}
                    onClick={() => {
                      setSelectedID(player.user_id);
                      const map = leafletRef.current;
                      if (map) {
                        try {
                          map.setView(projectWorldXY(player.x, player.y), Math.max(map.getZoom(), 3), {
                            animate: true,
                          });
                        } catch {
                          /* ignore */
                        }
                      }
                    }}
                  >
                    <span className="live-map-roster-name">{label}</span>
                    <span className="live-map-roster-meta">
                      {typeof player.ping === 'number' ? `${Math.round(player.ping)} ms` : '—'}
                      {player.level ? ` · Lv.${player.level}` : ''}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
          {selected ? (
            <div className="live-map-selected">
              <p>
                <strong>{selected.name || selected.user_id}</strong>
              </p>
              <p className="live-map-selected-coords">
                {Math.round(selected.x)}, {Math.round(selected.y)}
              </p>
              {onOpenPlayer ? (
                <button type="button" className="live-map-open-timeline" onClick={() => openSelected(selected)}>
                  打开轨迹时间轴
                </button>
              ) : null}
            </div>
          ) : (
            <p className="live-map-roster-hint">点击地图点或列表定位；可跳转该玩家时间轴。</p>
          )}
        </aside>
      </div>
    </section>
  );
}
