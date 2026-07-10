import type { OverrideRule, Rule } from '../api';
import { DurationField } from './DurationField';

const weekdays = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];

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
    <label className="switch-row"><span><strong>Enforce policy</strong><small>Track allowance and enforce the selected strategy.</small></span><input type="checkbox" checked={rule.enabled} onChange={(event) => patch({ enabled: event.target.checked })} /></label>
    <div className="form-grid">
      <label className="field"><span>Timezone</span><input value={timezone} onChange={(event) => onTimezone(event.target.value)} /></label>
      <StrategyField value={rule.strategy} onChange={(strategy) => patch({ strategy })} />
      {rule.strategy === 'fixed_window' && <>
        <label className="field"><span>Period</span><select value={rule.period} onChange={(event) => patch({ period: event.target.value })}><option value="daily">Daily</option><option value="weekly">Weekly</option></select></label>
        {rule.period === 'weekly' && <WeekdayField value={rule.reset_weekday ?? 'Monday'} onChange={(reset_weekday) => patch({ reset_weekday })} />}
        <label className="field"><span>Reset time</span><input type="time" value={rule.reset_at} onChange={(event) => patch({ reset_at: event.target.value })} /></label>
        <DurationField label="Fixed allowance" milliseconds={rule.limit_ms} onChange={(limit_ms) => patch({ limit_ms })} />
      </>}
      {rule.strategy === 'cooldown' && <>
        <DurationField label="Play duration" milliseconds={rule.cooldown_every_ms ?? 7_200_000} onChange={(cooldown_every_ms) => patch({ cooldown_every_ms })} />
        <DurationField label="Required rest" milliseconds={rule.cooldown_rest_ms ?? 1_800_000} onChange={(cooldown_rest_ms) => patch({ cooldown_rest_ms })} />
      </>}
      {rule.strategy === 'credit' && <>
        <DurationField label="Recovery interval" milliseconds={rule.credit_recover_every_ms ?? 3_600_000} onChange={(credit_recover_every_ms) => patch({ credit_recover_every_ms })} />
        <DurationField label="Recovery amount" milliseconds={rule.credit_recover_amount_ms ?? 1_800_000} onChange={(credit_recover_amount_ms) => patch({ credit_recover_amount_ms })} />
        <DurationField label="Maximum credit" milliseconds={rule.credit_max_ms ?? 10_800_000} onChange={(credit_max_ms) => patch({ credit_max_ms })} />
      </>}
    </div>
    <WarningsEditor values={rule.warning_before_ms} onChange={(warning_before_ms) => patch({ warning_before_ms })} />
  </div>;
}

function OverrideRuleForm({ rule, defaults, onChange }: OverrideProps) {
  const patch = (value: Partial<OverrideRule>) => onChange({ ...rule, ...value });
  const strategy = rule.strategy ?? defaults.strategy;
  const period = rule.period ?? defaults.period;
  const inherited = <option value="">Inherit global default</option>;
  return <div className="rule-form">
    <label className="switch-row"><span><strong>Exempt player</strong><small>Disable enforcement without discarding custom values.</small></span><input type="checkbox" checked={rule.exempt} onChange={(event) => patch({ exempt: event.target.checked })} /></label>
    <div className="form-grid">
      <label className="field"><span>Enabled state</span><select value={rule.enabled === undefined ? 'inherit' : rule.enabled ? 'enabled' : 'disabled'} onChange={(event) => {
        const value = event.target.value;
        patch({ enabled: value === 'inherit' ? undefined : value === 'enabled' });
      }}><option value="inherit">Inherit</option><option value="enabled">Enabled</option><option value="disabled">Disabled</option></select></label>
      <label className="field"><span>Strategy</span><select value={rule.strategy ?? ''} onChange={(event) => patch({ strategy: event.target.value || undefined })}>{inherited}<option value="fixed_window">Fixed allowance</option><option value="cooldown">Play then rest</option><option value="credit">Recovering credit</option></select></label>
      {strategy === 'fixed_window' && <>
        <label className="field"><span>Period</span><select value={rule.period ?? ''} onChange={(event) => patch({ period: event.target.value || undefined })}>{inherited}<option value="daily">Daily</option><option value="weekly">Weekly</option></select></label>
        {period === 'weekly' && <label className="field"><span>Reset weekday</span><select value={rule.reset_weekday ?? ''} onChange={(event) => patch({ reset_weekday: event.target.value || undefined })}>{inherited}{weekdays.map((day) => <option key={day}>{day}</option>)}</select></label>}
        <OptionalText label="Reset time" type="time" value={rule.reset_at} inherited={defaults.reset_at} onChange={(reset_at) => patch({ reset_at })} />
        <OptionalDuration label="Fixed allowance" value={rule.limit_ms} inherited={defaults.limit_ms} onChange={(limit_ms) => patch({ limit_ms })} />
      </>}
      {strategy === 'cooldown' && <>
        <OptionalDuration label="Play duration" value={rule.cooldown_every_ms} inherited={defaults.cooldown_every_ms ?? 7_200_000} onChange={(cooldown_every_ms) => patch({ cooldown_every_ms })} />
        <OptionalDuration label="Required rest" value={rule.cooldown_rest_ms} inherited={defaults.cooldown_rest_ms ?? 1_800_000} onChange={(cooldown_rest_ms) => patch({ cooldown_rest_ms })} />
      </>}
      {strategy === 'credit' && <>
        <OptionalDuration label="Recovery interval" value={rule.credit_recover_every_ms} inherited={defaults.credit_recover_every_ms ?? 3_600_000} onChange={(credit_recover_every_ms) => patch({ credit_recover_every_ms })} />
        <OptionalDuration label="Recovery amount" value={rule.credit_recover_amount_ms} inherited={defaults.credit_recover_amount_ms ?? 1_800_000} onChange={(credit_recover_amount_ms) => patch({ credit_recover_amount_ms })} />
        <OptionalDuration label="Maximum credit" value={rule.credit_max_ms} inherited={defaults.credit_max_ms ?? 10_800_000} onChange={(credit_max_ms) => patch({ credit_max_ms })} />
      </>}
    </div>
    <label className="inherit-row"><input type="checkbox" checked={rule.warning_before_ms !== undefined} onChange={(event) => patch({ warning_before_ms: event.target.checked ? [...defaults.warning_before_ms] : undefined })} />Custom warning thresholds</label>
    {rule.warning_before_ms !== undefined && <WarningsEditor values={rule.warning_before_ms} onChange={(warning_before_ms) => patch({ warning_before_ms })} />}
  </div>;
}

function StrategyField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <label className="field"><span>Strategy</span><select value={value} onChange={(event) => onChange(event.target.value)}><option value="fixed_window">Fixed allowance</option><option value="cooldown">Play then rest</option><option value="credit">Recovering credit</option></select></label>;
}

function WeekdayField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <label className="field"><span>Reset weekday</span><select value={value} onChange={(event) => onChange(event.target.value)}>{weekdays.map((day) => <option key={day}>{day}</option>)}</select></label>;
}

function OptionalDuration({ label, value, inherited, onChange }: { label: string; value?: number; inherited: number; onChange: (value?: number) => void }) {
  const custom = value !== undefined;
  return <div className="optional-field"><label className="inherit-row"><input type="checkbox" checked={custom} onChange={(event) => onChange(event.target.checked ? inherited : undefined)} />Custom {label.toLowerCase()}</label><DurationField label={label} milliseconds={value ?? inherited} disabled={!custom} onChange={onChange} /></div>;
}

function OptionalText({ label, type, value, inherited, onChange }: { label: string; type: string; value?: string; inherited: string; onChange: (value?: string) => void }) {
  const custom = value !== undefined;
  return <div className="optional-field"><label className="inherit-row"><input type="checkbox" checked={custom} onChange={(event) => onChange(event.target.checked ? inherited : undefined)} />Custom {label.toLowerCase()}</label><label className="field"><span>{label}</span><input type={type} value={value ?? inherited} disabled={!custom} onChange={(event) => onChange(event.target.value)} /></label></div>;
}

function WarningsEditor({ values, onChange }: { values: number[]; onChange: (values: number[]) => void }) {
  return <section className="warnings-editor"><div className="section-label"><strong>Warning thresholds</strong><button type="button" onClick={() => onChange([...values, 300_000].sort((a, b) => b - a))}>Add threshold</button></div><div className="warning-chips">{values.map((value, index) => <span key={`${value}-${index}`}><DurationField label={`Warning ${index + 1}`} milliseconds={value} onChange={(next) => onChange(values.map((item, itemIndex) => itemIndex === index ? next : item).sort((a, b) => b - a))} /><button type="button" aria-label={`Remove warning ${index + 1}`} onClick={() => onChange(values.filter((_, itemIndex) => itemIndex !== index))}>×</button></span>)}</div></section>;
}
