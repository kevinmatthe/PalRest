import type { OverrideRule, Rule } from '../api';
import { DurationField } from './DurationField';

const weekdays = [
  { value: 'Monday', label: '周一' },
  { value: 'Tuesday', label: '周二' },
  { value: 'Wednesday', label: '周三' },
  { value: 'Thursday', label: '周四' },
  { value: 'Friday', label: '周五' },
  { value: 'Saturday', label: '周六' },
  { value: 'Sunday', label: '周日' },
] as const;

type GlobalProps = {
  kind: 'global';
  timezone: string;
  rule: Rule;
  onTimezone: (value: string) => void;
  onChange: (rule: Rule) => void;
};

type OverrideProps = {
  kind: 'override';
  rule: OverrideRule;
  defaults: Rule;
  onChange: (rule: OverrideRule) => void;
};

export function RuleForm(props: GlobalProps | OverrideProps) {
  if (props.kind === 'global') return <GlobalRuleForm {...props} />;
  return <OverrideRuleForm {...props} />;
}

function GlobalRuleForm({ timezone, rule, onTimezone, onChange }: GlobalProps) {
  const patch = (value: Partial<Rule>) => onChange({ ...rule, ...value });
  return <div className="rule-form">
    <label className="switch-row"><span><strong>启用策略</strong><small>跟踪额度并按所选策略执行限制。</small></span><input type="checkbox" checked={rule.enabled} onChange={(event) => patch({ enabled: event.target.checked })} /></label>
    <div className="form-grid">
      <label className="field"><span>时区</span><input value={timezone} onChange={(event) => onTimezone(event.target.value)} /></label>
      <StrategyField value={rule.strategy} onChange={(strategy) => patch({ strategy })} />
      {rule.strategy === 'fixed_window' && <>
        <label className="field"><span>周期</span><select value={rule.period} onChange={(event) => patch({ period: event.target.value })}><option value="daily">每日</option><option value="weekly">每周</option></select></label>
        {rule.period === 'weekly' && <WeekdayField value={rule.reset_weekday ?? 'Monday'} onChange={(reset_weekday) => patch({ reset_weekday })} />}
        <label className="field"><span>重置时间</span><input type="time" value={rule.reset_at} onChange={(event) => patch({ reset_at: event.target.value })} /></label>
        <DurationField label="固定额度" milliseconds={rule.limit_ms} onChange={(limit_ms) => patch({ limit_ms })} />
      </>}
      {rule.strategy === 'cooldown' && <>
        <DurationField label="游玩时长" milliseconds={rule.cooldown_every_ms ?? 7_200_000} onChange={(cooldown_every_ms) => patch({ cooldown_every_ms })} />
        <DurationField label="强制休息" milliseconds={rule.cooldown_rest_ms ?? 1_800_000} onChange={(cooldown_rest_ms) => patch({ cooldown_rest_ms })} />
      </>}
      {rule.strategy === 'credit' && <>
        <DurationField label="恢复间隔" milliseconds={rule.credit_recover_every_ms ?? 3_600_000} onChange={(credit_recover_every_ms) => patch({ credit_recover_every_ms })} />
        <DurationField label="恢复量" milliseconds={rule.credit_recover_amount_ms ?? 1_800_000} onChange={(credit_recover_amount_ms) => patch({ credit_recover_amount_ms })} />
        <DurationField label="额度上限" milliseconds={rule.credit_max_ms ?? 10_800_000} onChange={(credit_max_ms) => patch({ credit_max_ms })} />
      </>}
    </div>
    <WarningsEditor values={rule.warning_before_ms} onChange={(warning_before_ms) => patch({ warning_before_ms })} />
  </div>;
}

function OverrideRuleForm({ rule, defaults, onChange }: OverrideProps) {
  const patch = (value: Partial<OverrideRule>) => onChange({ ...rule, ...value });
  const strategy = rule.strategy ?? defaults.strategy;
  const period = rule.period ?? defaults.period;
  const inherited = <option value="">继承全局默认</option>;
  return <div className="rule-form">
    <label className="switch-row"><span><strong>豁免玩家</strong><small>关闭执行限制，同时保留自定义配置。</small></span><input type="checkbox" checked={rule.exempt} onChange={(event) => patch({ exempt: event.target.checked })} /></label>
    <div className="form-grid">
      <label className="field"><span>启用状态</span><select value={rule.enabled === undefined ? 'inherit' : rule.enabled ? 'enabled' : 'disabled'} onChange={(event) => {
        const value = event.target.value;
        patch({ enabled: value === 'inherit' ? undefined : value === 'enabled' });
      }}><option value="inherit">继承</option><option value="enabled">启用</option><option value="disabled">禁用</option></select></label>
      <label className="field"><span>策略</span><select value={rule.strategy ?? ''} onChange={(event) => patch({ strategy: event.target.value || undefined })}>{inherited}<option value="fixed_window">固定额度</option><option value="cooldown">游玩后休息</option><option value="credit">恢复额度</option></select></label>
      {strategy === 'fixed_window' && <>
        <label className="field"><span>周期</span><select value={rule.period ?? ''} onChange={(event) => patch({ period: event.target.value || undefined })}>{inherited}<option value="daily">每日</option><option value="weekly">每周</option></select></label>
        {period === 'weekly' && <label className="field"><span>重置星期</span><select value={rule.reset_weekday ?? ''} onChange={(event) => patch({ reset_weekday: event.target.value || undefined })}>{inherited}{weekdays.map((day) => <option key={day.value} value={day.value}>{day.label}</option>)}</select></label>}
        <OptionalText label="重置时间" type="time" value={rule.reset_at} inherited={defaults.reset_at} onChange={(reset_at) => patch({ reset_at })} />
        <OptionalDuration label="固定额度" value={rule.limit_ms} inherited={defaults.limit_ms} onChange={(limit_ms) => patch({ limit_ms })} />
      </>}
      {strategy === 'cooldown' && <>
        <OptionalDuration label="游玩时长" value={rule.cooldown_every_ms} inherited={defaults.cooldown_every_ms ?? 7_200_000} onChange={(cooldown_every_ms) => patch({ cooldown_every_ms })} />
        <OptionalDuration label="强制休息" value={rule.cooldown_rest_ms} inherited={defaults.cooldown_rest_ms ?? 1_800_000} onChange={(cooldown_rest_ms) => patch({ cooldown_rest_ms })} />
      </>}
      {strategy === 'credit' && <>
        <OptionalDuration label="恢复间隔" value={rule.credit_recover_every_ms} inherited={defaults.credit_recover_every_ms ?? 3_600_000} onChange={(credit_recover_every_ms) => patch({ credit_recover_every_ms })} />
        <OptionalDuration label="恢复量" value={rule.credit_recover_amount_ms} inherited={defaults.credit_recover_amount_ms ?? 1_800_000} onChange={(credit_recover_amount_ms) => patch({ credit_recover_amount_ms })} />
        <OptionalDuration label="额度上限" value={rule.credit_max_ms} inherited={defaults.credit_max_ms ?? 10_800_000} onChange={(credit_max_ms) => patch({ credit_max_ms })} />
      </>}
    </div>
    <label className="inherit-row"><input type="checkbox" checked={rule.warning_before_ms !== undefined} onChange={(event) => patch({ warning_before_ms: event.target.checked ? [...defaults.warning_before_ms] : undefined })} />自定义提醒阈值</label>
    {rule.warning_before_ms !== undefined && <WarningsEditor values={rule.warning_before_ms} onChange={(warning_before_ms) => patch({ warning_before_ms })} />}
  </div>;
}

function StrategyField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <label className="field"><span>策略</span><select value={value} onChange={(event) => onChange(event.target.value)}><option value="fixed_window">固定额度</option><option value="cooldown">游玩后休息</option><option value="credit">恢复额度</option></select></label>;
}

function WeekdayField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <label className="field"><span>重置星期</span><select value={value} onChange={(event) => onChange(event.target.value)}>{weekdays.map((day) => <option key={day.value} value={day.value}>{day.label}</option>)}</select></label>;
}

function OptionalDuration({ label, value, inherited, onChange }: { label: string; value?: number; inherited: number; onChange: (value?: number) => void }) {
  const custom = value !== undefined;
  return <div className="optional-field"><label className="inherit-row"><input type="checkbox" checked={custom} onChange={(event) => onChange(event.target.checked ? inherited : undefined)} />自定义{label}</label><DurationField label={label} milliseconds={value ?? inherited} disabled={!custom} onChange={onChange} /></div>;
}

function OptionalText({ label, type, value, inherited, onChange }: { label: string; type: string; value?: string; inherited: string; onChange: (value?: string) => void }) {
  const custom = value !== undefined;
  return <div className="optional-field"><label className="inherit-row"><input type="checkbox" checked={custom} onChange={(event) => onChange(event.target.checked ? inherited : undefined)} />自定义{label}</label><label className="field"><span>{label}</span><input type={type} value={value ?? inherited} disabled={!custom} onChange={(event) => onChange(event.target.value)} /></label></div>;
}

function WarningsEditor({ values, onChange }: { values: number[]; onChange: (values: number[]) => void }) {
  return <section className="warnings-editor"><div className="section-label"><strong>提醒阈值</strong><button type="button" onClick={() => {
    const next = values.length === 0 ? 300_000 : Math.max(1, Math.floor(Math.min(...values) / 2));
    onChange([...values, next].sort((a, b) => b - a));
  }}>添加阈值</button></div><div className="warning-chips">{values.map((value, index) => <span key={`${value}-${index}`}><DurationField label={`提醒 ${index + 1}`} milliseconds={value} onChange={(next) => onChange(values.map((item, itemIndex) => itemIndex === index ? next : item).sort((a, b) => b - a))} /><button type="button" aria-label={`移除提醒 ${index + 1}`} onClick={() => onChange(values.filter((_, itemIndex) => itemIndex !== index))}>×</button></span>)}</div></section>;
}
