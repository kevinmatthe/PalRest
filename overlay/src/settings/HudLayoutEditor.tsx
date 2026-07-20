import type { ChangeEvent, ReactNode } from 'react'

import { OverlayBar } from '../components/OverlayBar'
import type { DisplayField, Presentation } from '../contracts/presentation'
import {
  cloneLayoutProfile,
  type LayoutProfile,
  type ReadonlyLayoutProfile,
  type SlotSelection,
} from '../core/layout'

export interface HudLayoutEditorProps {
  presentation: Presentation
  layout: LayoutProfile
  defaultLayout: ReadonlyLayoutProfile
  onChange: (layout: LayoutProfile) => void
  mapBaseUrl?: string
}

type FieldGroup = { label: string; fields: DisplayField[] }

const GROUPS = [
  { label: '身份', prefixes: ['identity.'] },
  { label: '在线与网络', prefixes: ['presence.', 'network.', 'location.'] },
  { label: '时长', prefixes: ['activity.'] },
  { label: '策略', prefixes: ['policy.'] },
] as const

function groupFields(fields: readonly DisplayField[]): FieldGroup[] {
  const used = new Set<string>()
  const groups: FieldGroup[] = GROUPS.map((group) => ({
    label: group.label,
    fields: fields.filter((field) => {
      const matches = group.prefixes.some((prefix) => field.id.startsWith(prefix))
      if (matches) used.add(field.id)
      return matches
    }),
  }))
  const other = fields.filter((field) => !used.has(field.id))
  if (other.length) groups.push({ label: '其他', fields: other })
  return groups
}

function fieldCopy(field: DisplayField): string {
  return `${field.label}${field.available ? '' : '（当前不可用）'}`
}

function FieldOptions({
  fields,
  selected,
  disabled,
}: {
  fields: readonly DisplayField[]
  selected: string
  disabled?: string
}) {
  const known = fields.some(({ id }) => id === selected)
  return <>
    {!known ? <optgroup label="已保存布局"><option value={selected}>{selected}（目录暂未提供）</option></optgroup> : null}
    {groupFields(fields).map((group) => group.fields.length ? (
      <optgroup label={group.label} key={group.label}>
        {group.fields.map((field) => (
          <option key={field.id} value={field.id} disabled={field.id === disabled}>{fieldCopy(field)}</option>
        ))}
      </optgroup>
    ) : null)}
  </>
}

function SlotRow({
  index,
  fields,
  value,
  onChange,
}: {
  index: number
  fields: readonly DisplayField[]
  value: SlotSelection
  onChange: (value: SlotSelection) => void
}) {
  const update = (part: keyof SlotSelection) => (event: ChangeEvent<HTMLSelectElement>) => {
    onChange({ ...value, [part]: event.target.value })
  }
  return (
    <fieldset className="hud-editor__slot" aria-label={`槽位 ${index + 1}`}>
      <legend>槽位 {index + 1}</legend>
      <label><span>主字段</span><select aria-label={`槽位 ${index + 1} 主字段`} value={value.primary} onChange={update('primary')}>
        <FieldOptions fields={fields} selected={value.primary} disabled={value.fallback} />
      </select></label>
      <label><span>后备字段</span><select aria-label={`槽位 ${index + 1} 后备字段`} value={value.fallback} onChange={update('fallback')}>
        <FieldOptions fields={fields} selected={value.fallback} disabled={value.primary} />
      </select></label>
    </fieldset>
  )
}

function LeftOption({ value, children }: { value: string; children: ReactNode }) {
  return <option value={value}>{children}</option>
}

function hasLegalProgress(field: DisplayField): boolean {
  return field.available && typeof field.progress === 'number' &&
    Number.isFinite(field.progress) && field.progress >= 0 && field.progress <= 1
}

export function HudLayoutEditor({ presentation, layout, defaultLayout, onChange, mapBaseUrl }: HudLayoutEditorProps) {
  const updateSlot = (index: number, value: SlotSelection) => {
    const next = cloneLayoutProfile(layout)
    next.slots[index] = value
    onChange(next)
  }
  const progressFields = presentation.fields.filter(hasLegalProgress)
  const progressFieldKnown = layout.progress.field !== undefined &&
    progressFields.some(({ id }) => id === layout.progress.field)

  return (
    <div className="hud-editor">
      <div className="hud-editor__preview" aria-label="HUD 实时预览">
        <span className="hud-editor__eyebrow">实时预览</span>
        <div className="hud-editor__preview-stage">
          <OverlayBar presentation={presentation} layout={layout} status="ready" adjustMode={false} scale={1} mapBaseUrl={mapBaseUrl} />
        </div>
      </div>

      <div className="hud-editor__controls">
        <fieldset className="hud-editor__left">
          <legend>左侧模块</legend>
          <label><span>主模块</span><select aria-label="左侧主模块" value={layout.left.primary} onChange={(event) => {
            const next = cloneLayoutProfile(layout)
            const primary = event.target.value as LayoutProfile['left']['primary']
            next.left = primary === layout.left.fallback
              ? { primary, fallback: layout.left.primary }
              : { ...layout.left, primary }
            onChange(next)
          }}>
            <LeftOption value="map">小地图</LeftOption>
            <LeftOption value="player_badge">玩家徽章</LeftOption>
          </select></label>
          <label><span>后备模块</span><select aria-label="左侧后备模块" value={layout.left.fallback} onChange={(event) => {
            const next = cloneLayoutProfile(layout)
            const fallback = event.target.value as LayoutProfile['left']['fallback']
            next.left = fallback === layout.left.primary
              ? { primary: layout.left.fallback, fallback }
              : { ...layout.left, fallback }
            onChange(next)
          }}>
            <LeftOption value="map">小地图</LeftOption>
            <LeftOption value="player_badge">玩家徽章</LeftOption>
          </select></label>
        </fieldset>

        <div className="hud-editor__slots">
          {layout.slots.map((slot, index) => <SlotRow key={index} index={index} fields={presentation.fields} value={slot} onChange={(value) => updateSlot(index, value)} />)}
        </div>

        <fieldset className="hud-editor__progress">
          <legend>底部进度轨</legend>
          <label><span>模式</span><select aria-label="进度轨模式" value={layout.progress.mode} onChange={(event) => {
            const mode = event.target.value as LayoutProfile['progress']['mode']
            if (mode === 'field' && progressFields.length === 0) return
            const next = cloneLayoutProfile(layout)
            next.progress = mode === 'hidden' ? { mode } : {
              mode,
              ...(mode === 'field' ? { field: progressFields[0]?.id ?? layout.progress.field } :
                layout.progress.field === undefined ? {} : { field: layout.progress.field }),
            }
            onChange(next)
          }}>
            <option value="auto">自动</option><option value="field" disabled={progressFields.length === 0}>指定字段</option><option value="hidden">隐藏</option>
          </select></label>
          {layout.progress.mode === 'field' ? <label><span>字段</span><select aria-label="进度字段" value={layout.progress.field ?? ''} onChange={(event) => {
            if (!progressFields.some(({ id }) => id === event.target.value)) return
            const next = cloneLayoutProfile(layout)
            next.progress = { mode: 'field', field: event.target.value }
            onChange(next)
          }} disabled={progressFields.length === 0}>
            {!progressFieldKnown && layout.progress.field !== undefined ? (
              <option value={layout.progress.field} disabled>{layout.progress.field}（目录暂未提供）</option>
            ) : null}
            {progressFields.length === 0 && layout.progress.field === undefined ? <option value="" disabled>暂无可用进度字段</option> : null}
            {progressFields.map((field) => <option key={field.id} value={field.id}>{fieldCopy(field)}</option>)}
          </select></label> : null}
        </fieldset>

        <p className="hud-editor__hint">主字段暂不可用时显示后备字段；两者均不可用时保留槽位并显示 “--”。</p>
        <button className="settings-button settings-button--ghost hud-editor__reset" type="button" onClick={() => onChange(cloneLayoutProfile(defaultLayout))}>
          恢复当前游戏默认布局
        </button>
      </div>
    </div>
  )
}
