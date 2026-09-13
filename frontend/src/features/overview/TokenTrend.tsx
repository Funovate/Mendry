import { useEffect, useRef, useState } from "react";
import type { Overview } from "./api";
import { dateTime, number } from "./format";

export function TokenTrend({ overview }: { overview: Overview }) {
  const { trend, timezone } = overview;
  const max = Math.max(1, ...trend.map((bucket) => bucket.input + bucket.output));
  const container = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);
  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(240, entry.contentRect.width)));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const height = 210, left = 64, bottom = 36;
  const slot = (width - left - 12) / Math.max(1, trend.length);
  const label = (value: string) => new Intl.DateTimeFormat("en-US", { timeZone: timezone,
    ...(overview.range === "today" ? { hour: "2-digit", hour12: false } : { month: "short", day: "numeric" }),
  }).format(new Date(value));
  return <>
    <div className="overview-trend-legend"><span><i className="input" />Input</span><span><i className="output" />Output</span><span title={`Accurate collection from ${dateTime(overview.collectionStartedAt, timezone)}. Earlier periods have no time-series data; the first bucket may be partial.`}>Since {new Intl.DateTimeFormat("en-US", { timeZone: timezone, month: "short", day: "numeric" }).format(new Date(overview.collectionStartedAt))}</span></div>
    <div ref={container}>
    <svg className="overview-trend" viewBox={`0 0 ${width} ${height + bottom}`} role="img" aria-label="Input and output Token consumption over time">
      {[0, 0.5, 1].map((step) => <g key={step}><line x1={left} x2={width} y1={height - step * (height - 20)} y2={height - step * (height - 20)} stroke="#e4e9e0" /><text x={left - 10} y={height - step * (height - 20) + 4} textAnchor="end">{number(Math.round(max * step))}</text></g>)}
      {trend.map((bucket, index) => {
        const x = left + slot * index + slot * 0.2;
        const inHeight = bucket.input / max * (height - 20), outHeight = bucket.output / max * (height - 20);
        const labelEvery = Math.max(1, Math.ceil(trend.length / Math.max(2, Math.floor((width - left) / 80))));
        const showLabel = index % labelEvery === 0;
        const description = `${dateTime(bucket.start, timezone)}: ${bucket.covered ? `${number(bucket.input)} input, ${number(bucket.output)} output${bucket.partial ? ", partial coverage" : ""}` : "No time-series data"}`;
        return <g key={bucket.start} tabIndex={0} role="img" aria-label={description}>
          <title>{description}</title>
          <rect x={x} y={10} width={slot * 0.6} height={height - 10} fill="transparent" />
          {bucket.covered ? <>
            <rect x={x} y={height - inHeight} width={slot * 0.6} height={inHeight} rx={2} fill="#176d52" opacity={bucket.partial ? 0.65 : 1} />
            <rect x={x} y={height - inHeight - outHeight} width={slot * 0.6} height={outHeight} rx={2} fill="#c1893e" opacity={bucket.partial ? 0.65 : 1} />
            {inHeight + outHeight === 0 && <line x1={x} x2={x + slot * 0.6} y1={height} y2={height} stroke="#176d52" strokeWidth={2} />}
          </> : <text x={x + slot * 0.3} y={height - 6} textAnchor="middle">—</text>}
          {showLabel && <text x={x + slot * 0.3} y={height + 22} textAnchor="middle">{label(bucket.start)}</text>}
        </g>;
      })}
    </svg>
    </div>
    <details className="overview-trend-data"><summary>View consumption data</summary><div className="overview-table-scroll"><table><thead><tr><th>Period start ({timezone})</th><th>Input</th><th>Output</th><th>Total</th><th>Coverage</th></tr></thead><tbody>{trend.map((bucket) => <tr key={bucket.start}><td>{dateTime(bucket.start, timezone)}</td><td>{bucket.covered ? number(bucket.input) : "—"}</td><td>{bucket.covered ? number(bucket.output) : "—"}</td><td>{bucket.covered ? number(bucket.input + bucket.output) : "—"}</td><td>{!bucket.covered ? "No data" : bucket.partial ? "Partial" : "Collected"}</td></tr>)}</tbody></table></div></details>
  </>;
}
