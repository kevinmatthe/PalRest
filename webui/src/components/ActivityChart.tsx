import { useEffect, useMemo, useRef, useState } from 'react';

type LinePoint = { at: string; value: number | null; max?: number | null; coverage?: number };
type BarPoint = { date: string; value: number };

export type ActivityChartProps =
  | { kind: 'line'; label: string; points: LinePoint[] }
  | { kind: 'bar'; label: string; points: BarPoint[] };

type Geometry =
  | { kind: 'line'; segments: string[]; singletons: { x: number; y: number }[] }
  | { kind: 'bar'; bars: { x: number; y: number; width: number; height: number }[] };

const WIDTH = 100;
const HEIGHT = 40;
const round = (value: number) => Math.round(value * 100) / 100;

function lineGeometry(points: LinePoint[]): Geometry {
  let maximum = 1;
  for (const point of points) if (point.value !== null) maximum = Math.max(maximum, point.value);
  const xFor = (index: number) => points.length <= 1 ? WIDTH / 2 : index * WIDTH / (points.length - 1);
  const runs: { x: number; y: number }[][] = [];
  let run: { x: number; y: number }[] = [];
  points.forEach((point, index) => {
    if (point.value === null) {
      if (run.length) runs.push(run);
      run = [];
      return;
    }
    run.push({ x: round(xFor(index)), y: round(HEIGHT - point.value / maximum * HEIGHT) });
  });
  if (run.length) runs.push(run);
  return {
    kind: 'line',
    segments: runs.map((items) => items.map((point, index) => `${index ? 'L' : 'M'} ${point.x} ${point.y}`).join(' ')),
    singletons: runs.filter((items) => items.length === 1).map((items) => items[0]),
  };
}

function barGeometry(points: BarPoint[]): Geometry {
  let maximum = 1;
  for (const point of points) maximum = Math.max(maximum, point.value);
  const slot = WIDTH / Math.max(points.length, 1);
  const barWidth = slot * 0.72;
  return {
    kind: 'bar',
    bars: points.map((point, index) => {
      const height = point.value / maximum * HEIGHT;
      return { x: round(index * slot + (slot - barWidth) / 2), y: round(HEIGHT - height), width: round(barWidth), height: round(height) };
    }),
  };
}

function ChartLayer({ geometry, previous = false }: { geometry: Geometry; previous?: boolean }) {
  return <g className={previous ? 'activity-chart__previous' : 'activity-chart__current'} aria-hidden="true">
    {geometry.kind === 'line' ? <>
      {geometry.segments.map((path, index) => <path data-testid="line-segment" d={path} key={`${path}-${index}`} fill="none" vectorEffect="non-scaling-stroke" />)}
      {geometry.singletons.map((point, index) => <circle key={index} cx={point.x} cy={point.y} r="1.5" vectorEffect="non-scaling-stroke" />)}
    </> : geometry.bars.map((bar, index) => <rect data-testid="bar" key={index} {...bar} />)}
  </g>;
}

export function ActivityChart(props: ActivityChartProps) {
  const serialized = JSON.stringify(props.points);
  const geometry = useMemo(() => props.kind === 'line' ? lineGeometry(props.points) : barGeometry(props.points), [props.kind, serialized]);
  const currentRef = useRef(geometry);
  const serializedRef = useRef(serialized);
  const [previous, setPrevious] = useState<Geometry | null>(null);
  const [updating, setUpdating] = useState(false);
  const [showData, setShowData] = useState(false);

  useEffect(() => {
    if (serializedRef.current === serialized) return;
    const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
    serializedRef.current = serialized;
    if (reducedMotion) {
      currentRef.current = geometry;
      setPrevious(null);
      setUpdating(false);
      return;
    }
    setPrevious(currentRef.current);
    currentRef.current = geometry;
    setUpdating(true);
    const timer = window.setTimeout(() => {
      setUpdating(false);
      setPrevious(null);
    }, 550);
    return () => window.clearTimeout(timer);
  }, [geometry, serialized]);

  const description = useMemo(() => {
    if (props.kind === 'line') {
      let observed = 0;
      let minimum = Infinity;
      let maximum = -Infinity;
      for (const point of props.points) {
        if (point.value === null) continue;
        observed += 1;
        minimum = Math.min(minimum, point.value);
        maximum = Math.max(maximum, point.value);
      }
      const missing = props.points.length - observed;
      const range = observed ? ` Minimum ${minimum}; maximum ${maximum}.` : '';
      return `${props.points.length} points; ${observed} observed; ${missing} missing.${range}`;
    }
    let total = 0;
    let minimum = Infinity;
    let maximum = -Infinity;
    for (const point of props.points) {
      total += point.value;
      minimum = Math.min(minimum, point.value);
      maximum = Math.max(maximum, point.value);
    }
    const range = props.points.length ? ` Minimum ${minimum} ms; maximum ${maximum} ms.` : '';
    return `${props.points.length} points; total ${total} ms.${range}`;
  }, [props.kind, serialized]);

  return <div className={`activity-chart${updating ? ' is-updating' : ''}`}>
    <svg role="img" aria-label={props.label} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} width="100%" preserveAspectRatio="none">
      <title>{props.label}</title>
      <desc>{description}</desc>
      {previous ? <ChartLayer geometry={previous} previous /> : null}
      <ChartLayer geometry={geometry} />
    </svg>
    <button className="activity-chart__data-toggle" type="button" aria-expanded={showData} onClick={() => setShowData((visible) => !visible)}>
      {showData ? 'Hide data table' : 'Show data table'}
    </button>
    {showData ? <div className="activity-chart__data">
      <table>
        <caption>{props.label} data</caption>
        <thead><tr><th scope="col">{props.kind === 'line' ? 'Time' : 'Date'}</th><th scope="col">Value</th></tr></thead>
        <tbody>{props.kind === 'line'
          ? props.points.map((point, index) => <tr key={`${point.at}-${index}`}><th scope="row">{point.at}</th><td>{point.value === null ? 'Missing' : point.value}</td></tr>)
          : props.points.map((point, index) => <tr key={`${point.date}-${index}`}><th scope="row">{point.date}</th><td>{point.value} ms</td></tr>)
        }</tbody>
      </table>
    </div> : null}
  </div>;
}
