import { Link } from "react-router-dom";
import { Eye, Video, Trophy, TrendingUp, TrendingDown } from "lucide-react";
import type { ElementType } from "react";
import { formatNumber } from "./ChannelsPerformanceChart";
import type { RankingItem, RankingValueLabel, Rankings } from "./channelsPerformanceTypes";

export function RankingCard({
  title,
  icon: Icon,
  items,
  valueLabel,
}: {
  title: string;
  icon: ElementType;
  items: RankingItem[];
  valueLabel: RankingValueLabel;
}) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
      <div className="flex items-center gap-2 mb-4">
        <Icon size={18} className="text-[#9aa0aa]" />
        <h3 className="text-[15px] font-bold text-white">{title}</h3>
      </div>
      <div className="space-y-2">
        {items.slice(0, 5).map((item, index) => (
          <div
            key={item.id}
            className="flex items-center justify-between py-2 px-3 rounded-xl bg-white/[0.03] border border-white/[0.06]"
          >
            <div className="flex items-center gap-3 min-w-0">
              <span className="w-5 h-5 rounded-full bg-white/[0.08] text-[11px] font-bold text-white flex items-center justify-center shrink-0">
                {index + 1}
              </span>
              <Link
                to={`/app/dashboard-channels/${item.id}`}
                className="text-[13px] font-medium text-white truncate hover:text-[#9aa0aa] transition-colors no-underline"
              >
                @{item.username}
              </Link>
            </div>
            <span className="text-[13px] font-semibold text-white tabular-nums">
              {(() => {
                switch (valueLabel) {
                  case "percent":
                    return `${(item.value / 10).toFixed(1)}%`;
                  case "engagement":
                    return `${(item.value / 10).toFixed(1)} /video`;
                  default:
                    return formatNumber(item.value);
                }
              })()}
            </span>
          </div>
        ))}
        {items.length === 0 && (
          <p className="text-[13px] text-[#9aa0aa]">No data yet.</p>
        )}
      </div>
    </div>
  );
}


export function ChannelsPerformanceRankings({ rankings }: { rankings?: Rankings }) {
  if (!rankings) return null;
  return (
    <>
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6 mb-8">
        <RankingCard title="Top by subscribers" icon={Trophy} items={rankings.by_subscribers} valueLabel="subscribers" />
        <RankingCard title="Top by views" icon={Eye} items={rankings.by_views} valueLabel="views" />
        <RankingCard title="Top by videos" icon={Video} items={rankings.by_videos} valueLabel="videos" />
        <RankingCard title="Fastest growing (views)" icon={TrendingUp} items={rankings.fastest_growing_views} valueLabel="percent" />
        <RankingCard title="Top engagement" icon={TrendingUp} items={rankings.top_engagement} valueLabel="engagement" />
      </div>
      <h2 className="text-[16px] font-bold text-white mb-4">Bottom performers</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6 mb-8">
        <RankingCard title="Bottom by subscribers" icon={TrendingDown} items={rankings.bottom_subscribers} valueLabel="subscribers" />
        <RankingCard title="Bottom by views" icon={TrendingDown} items={rankings.bottom_views} valueLabel="views" />
        <RankingCard title="Bottom engagement" icon={TrendingDown} items={rankings.bottom_engagement} valueLabel="engagement" />
        <RankingCard title="Slowest growing (subscribers)" icon={TrendingDown} items={rankings.bottom_growing_subscribers} valueLabel="percent" />
        <RankingCard title="Slowest growing (views)" icon={TrendingDown} items={rankings.bottom_growing_views} valueLabel="percent" />
      </div>
    </>
  );
}
