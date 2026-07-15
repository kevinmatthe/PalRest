import { fromMilliseconds, toMilliseconds, type DurationUnit } from '../duration';

type Props = {
  label: string;
  milliseconds: number;
  onChange: (milliseconds: number) => void;
  disabled?: boolean;
};

export function DurationField({ label, milliseconds, onChange, disabled = false }: Props) {
  const duration = fromMilliseconds(milliseconds);
  const id = `duration-${label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`;
  const update = (value: number, unit: DurationUnit) => {
    if (value > 0) onChange(toMilliseconds(value, unit));
  };
  return <label className="field" htmlFor={id}>
    <span>{label}</span>
    <span className="duration-control">
      <input id={id} type="number" min="0.01" step="0.25" value={duration.value} disabled={disabled} onChange={(event) => update(event.currentTarget.valueAsNumber, duration.unit)} />
      <select aria-label={`${label}单位`} value={duration.unit} disabled={disabled} onChange={(event) => update(duration.value, event.currentTarget.value as DurationUnit)}>
        <option value="minutes">分钟</option>
        <option value="hours">小时</option>
      </select>
    </span>
  </label>;
}
