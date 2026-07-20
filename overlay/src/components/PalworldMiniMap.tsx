import { useEffect, useMemo, useRef, useState } from 'react'
import * as L from 'leaflet'
import 'leaflet/dist/leaflet.css'

import type { PresentationMap } from '../contracts/presentation'
import {
  PALWORLD_PROJECTION_ID,
  PALWORLD_TILE_BOUNDS,
  PALWORLD_TILE_SET_ID,
  type LeafletSimpleCoordinate,
  projectPalworldWorldToLeaflet,
  resolvePrivateTileUrl,
} from '../games/palworld/map'

const FIXED_ZOOM = 0

export interface PalworldMiniMapProps {
  map: PresentationMap
  serviceBaseUrl: string
  className?: string
  onUnavailable?: () => void
}

function coordinateFor(map: PresentationMap): LeafletSimpleCoordinate | null {
  try {
    return projectPalworldWorldToLeaflet(map.x, map.y)
  } catch {
    return null
  }
}

export function PalworldMiniMap({
  map: mapPosition,
  serviceBaseUrl,
  className,
  onUnavailable,
}: PalworldMiniMapProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const leafletMapRef = useRef<L.Map | null>(null)
  const markerRef = useRef<L.CircleMarker | null>(null)
  const currentCoordinateRef = useRef<LeafletSimpleCoordinate | null>(null)
  const desiredCoordinateRef = useRef<LeafletSimpleCoordinate | null>(null)
  const [tileUnavailable, setTileUnavailable] = useState(false)

  const coordinate = coordinateFor(mapPosition)
  desiredCoordinateRef.current = coordinate
  const supported = mapPosition.projection === PALWORLD_PROJECTION_ID &&
    mapPosition.tile_set === PALWORLD_TILE_SET_ID
  const resolvedTileUrl = useMemo(
    () => resolvePrivateTileUrl(mapPosition.tile_url, serviceBaseUrl),
    [mapPosition.tile_url, serviceBaseUrl],
  )
  const mapReady = supported && resolvedTileUrl !== null && coordinate !== null

  useEffect(() => {
    let active = true
    setTileUnavailable(false)
    const container = containerRef.current
    const initialCoordinate = desiredCoordinateRef.current
    if (!mapReady || container === null || resolvedTileUrl === null || initialCoordinate === null) {
      return
    }

    let leafletMap: L.Map | null = null
    let tileLayer: L.TileLayer | null = null
    let marker: L.CircleMarker | null = null
    const handleTileError = () => {
      if (!active) return
      setTileUnavailable(true)
      onUnavailable?.()
    }

    try {
      leafletMap = L.map(container, {
        crs: L.CRS.Simple,
        attributionControl: false,
        zoomControl: false,
        dragging: false,
        doubleClickZoom: false,
        scrollWheelZoom: false,
        boxZoom: false,
        keyboard: false,
        touchZoom: false,
      })
      tileLayer = L.tileLayer(resolvedTileUrl, {
        bounds: PALWORLD_TILE_BOUNDS,
        noWrap: true,
        minZoom: FIXED_ZOOM,
        maxZoom: FIXED_ZOOM,
        minNativeZoom: FIXED_ZOOM,
        maxNativeZoom: FIXED_ZOOM,
      })
      marker = L.circleMarker(initialCoordinate, {
        interactive: false,
        radius: 3,
        color: '#eef8f7',
        weight: 1,
        fillColor: '#55e6df',
        fillOpacity: 1,
      })

      tileLayer.on('tileerror', handleTileError)
      tileLayer.addTo(leafletMap)
      marker.addTo(leafletMap)
      leafletMap.setView(initialCoordinate, FIXED_ZOOM, { animate: false })
      leafletMapRef.current = leafletMap
      markerRef.current = marker
      currentCoordinateRef.current = initialCoordinate
    } catch {
      if (active) {
        setTileUnavailable(true)
        onUnavailable?.()
      }
      tileLayer?.off('tileerror', handleTileError)
      marker?.remove()
      tileLayer?.remove()
      leafletMap?.remove()
      return () => { active = false }
    }

    return () => {
      active = false
      tileLayer?.off('tileerror', handleTileError)
      marker?.remove()
      tileLayer?.remove()
      leafletMap?.remove()
      if (leafletMapRef.current === leafletMap) leafletMapRef.current = null
      if (markerRef.current === marker) markerRef.current = null
      currentCoordinateRef.current = null
    }
  }, [mapReady, mapPosition.projection, mapPosition.tile_set, onUnavailable, resolvedTileUrl, serviceBaseUrl])

  useEffect(() => {
    const leafletMap = leafletMapRef.current
    const marker = markerRef.current
    const current = currentCoordinateRef.current
    if (leafletMap === null || marker === null || coordinate === null) return
    if (current?.[0] === coordinate[0] && current[1] === coordinate[1]) return

    marker.setLatLng(coordinate)
    leafletMap.setView(coordinate, FIXED_ZOOM, { animate: false })
    currentCoordinateRef.current = coordinate
  }, [coordinate?.[0], coordinate?.[1]])

  const unavailable = !mapReady || tileUnavailable
  const classes = ['overlay__locator', className].filter(Boolean).join(' ')

  return (
    <div
      className={classes}
      data-testid="capability-map"
      data-capability="map"
      style={{ pointerEvents: 'none' }}
    >
      <div
        ref={containerRef}
        data-testid="palworld-mini-map-canvas"
        aria-hidden="true"
        style={{ position: 'absolute', inset: 0 }}
      />
      {unavailable ? (
        <span
          role="status"
          style={{
            position: 'absolute',
            inset: 0,
            zIndex: 1,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'rgba(220, 233, 232, 0.58)',
            background: '#040c0f',
            fontSize: '0.625rem',
          }}
        >
          地图不可用
        </span>
      ) : null}
    </div>
  )
}
