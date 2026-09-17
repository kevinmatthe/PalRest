import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useWindowVisibility } from './useWindowVisibility'
import type { DesktopBridge } from './bridge'
afterEach(cleanup)
it('does not let a failed initial query override a newer native hide event', async () => {
  let changed!: (visible: boolean) => void
  let reject!: (error: Error) => void
  const bridge = { onOverlayVisibilityChanged: vi.fn(async (handler: (visible: boolean) => void) => { changed = handler; return () => {} }),
    isOverlayVisible: vi.fn(() => new Promise<boolean>((_resolve, fail) => { reject = fail })) } as unknown as DesktopBridge
  const { result } = renderHook(() => useWindowVisibility(bridge))
  await act(async () => {})
  await act(async () => { changed(false); reject(new Error('query failed')) })
  expect(result.current).toBe(false)
})
