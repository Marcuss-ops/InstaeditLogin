import { Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { TrendPoint } from "./channelsPerformanceTypes";

export function formatNumber(value: number | string): string {
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return String(value);
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

function formatTrendDate(value: string) {
  const d = new Date(value + "T00:00:00Z");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function TrendChart({
  title,
  data,
  dataKey,
  color,
  valueFormatter = formatNumber,
  axisFormatter = valueFormatter,
}: {
  title: string;
  data: TrendPoint[];
  dataKey: "subscribers" | "views" | "engagement";
  color: string;
  valueFormatter?: (value: number) => string;
  axisFormatter?: (value: number) => string;
}) {
  if (data.length === 0) {
    return (
      <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
        <h2 className="text-[16px] font-bold text-white mb-4">{title}</h2>
        <div className="h-64 flex items-center justify-center rounded-2xl border border-dashed border-white/[0.12]">
          <p className="text-[13px] text-[#9aa0aa]">No trend data yet.</p>
        </div>
      </div>
    );
  }
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
      <h2 className="text-[16px] font-bold text-white mb-4">{title}</h2>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} syncId="trend-charts" margin={{ top: 5, right: 5, left: -10, bottom: 0 }}>
            <defs>
              <linearGradient id={`${dataKey}Gradient`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={color} stopOpacity={0.4} />
                <stop offset="100%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid stroke="rgba(255,255,255,0.06)" vertical={false} />
            <XAxis
              dataKey="date"
              tick={{ fill: "#9aa0aa", fontSize: 12 }}
              axisLine={{ stroke: "rgba(255,255,255,0.12)" }}
              tickFormatter={formatTrendDate}
              tickLine={false}
              minTickGap={16}
            />
            <YAxis
              tick={{ fill: "#9aa0aa", fontSize: 12 }}
              axisLine={false}
              tickLine={false}
              tickFormatter={axisFormatter}
            />
            <Tooltip
              contentStyle={{
                background: "#1f1f2e",
                border: "1px solid rgba(255,255,255,0.12)",
                borderRadius: 12,
                color: "#e8e8ef",
              }}
              labelFormatter={(label) => formatTrendDate(String(label))}
              formatter={(value) => [valueFormatter(Number(value)), title]}
            />
            <Area
              type="monotone"
              dataKey={dataKey}
              stroke={color}
              fill={`url(#${dataKey}Gradient)`}
              strokeWidth={2}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}


export function TopSubscribersChart({ data }: { data: Array<{ name: string; value: number }> }) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
      <h2 className="text-[16px] font-bold text-white mb-4">Top channels by subscribers</h2>
      <div className="h-72">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} layout="vertical" margin={{ top: 5, right: 30, left: 40, bottom: 5 }}>
            <CartesianGrid stroke="rgba(255,255,255,0.06)" horizontal={false} />
            <XAxis type="number" tick={{ fill: "#9aa0aa", fontSize: 12 }} axisLine={{ stroke: "rgba(255,255,255,0.12)" }} tickFormatter={formatNumber} tickLine={false} />
            <YAxis type="category" dataKey="name" tick={{ fill: "#9aa0aa", fontSize: 12 }} axisLine={{ stroke: "rgba(255,255,255,0.12)" }} tickLine={false} width={80} />
            <Tooltip contentStyle={{ background: "#1f1f2e", border: "1px solid rgba(255,255,255,0.12)", borderRadius: 12, color: "#e8e8ef" }} formatter={(value) => { const num = typeof value === "number" ? value : Number(value ?? 0); return [formatNumber(num), "Subscribers"]; }} />
            <Bar dataKey="value" radius={[0, 6, 6, 0]}>
              {data.map((_, index) => <Cell key={`cell-${index}`} fill={index === 0 ? "#fbbf24" : "#a78bfa"} />)}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}
