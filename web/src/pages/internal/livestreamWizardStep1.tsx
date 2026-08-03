import { Link } from "react-router-dom";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Radio,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import { EmptyState } from "../../components/feedback/EmptyState";
import { ErrorState, Skeleton } from "../../components/feedback";
import { cn } from "../../lib/utils";
import { formatLastVerified } from "./livestreamsVisual";
import type { LivestreamChannel } from "./livestreamsTypes";
import type { LivestreamChannelsState } from "./useLivestreamChannels";

/**
 * Wizard step 1 of 5 (Canale) — channel selection with the preflight
 * data from GET /api/v1/livestreams/channels: OAuth grant state, live
 * scope presence, last validation and active lives. A channel can only
 * be selected when its grant is ready AND carries a YouTube live scope;
 * anything else is shown with an explicit reason and the "Continua"
 * button stays blocked.
 */
export function LiveStreamWizardStep1({
  state,
  reload,
  channels,
  selectedID,
  onSelect,
  onContinue,
}: {
  state: LivestreamChannelsState;
  reload: () => void;
  channels: LivestreamChannel[];
  selectedID: number | null;
  onSelect: (id: number) => void;
  onContinue: () => void;
}) {
  const selected = channels.find((channel) => channel.platform_account_id === selectedID) ?? null;
  const eligibleCount = channels.filter(isChannelEligible).length;

  return (
    <>
      <section className="mt-8">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-[15px] font-bold text-white">Canali YouTube del workspace</h2>
          {state.kind === "ready" && (
            <span className="text-[11px] text-[#9aa0aa]">
              {eligibleCount} di {channels.length} pronti per il live
            </span>
          )}
        </div>

        {state.kind === "loading" && (
          <div className="space-y-3">
            <Skeleton variant="card" height={104} />
            <Skeleton variant="card" height={104} />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Impossibile caricare i canali"
            message={state.message}
            onRetry={() => void reload()}
            retryLabel="Riprova"
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && channels.length === 0 && (
          <EmptyState
            icon={<Radio size={28} />}
            title="Nessun canale YouTube nel workspace"
            description="Collega e attiva un canale YouTube dalla pagina Canali, poi torna qui per creare la tua prima live."
            cta={
              <Link
                to="/app/linking"
                className="inline-flex items-center gap-2 rounded-xl bg-violet-500 px-4 py-2 text-[13px] font-bold text-white no-underline transition-colors hover:bg-violet-400"
              >
                Vai ai canali <ArrowRight size={14} aria-hidden="true" />
              </Link>
            }
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && channels.length > 0 && (
          <div className="space-y-2.5" role="radiogroup" aria-label="Scegli il canale YouTube">
            {channels.map((channel) => (
              <ChannelCard
                key={channel.platform_account_id}
                channel={channel}
                selected={selectedID === channel.platform_account_id}
                onSelect={isChannelEligible(channel) ? () => onSelect(channel.platform_account_id) : undefined}
              />
            ))}
          </div>
        )}
      </section>

      {/* Bottom action bar */}
      <div className="sticky bottom-4 mt-8 flex flex-col gap-3 rounded-2xl border border-white/[0.10] bg-[#14141e]/95 p-4 shadow-[0_-8px_40px_rgba(0,0,0,0.5)] backdrop-blur sm:flex-row sm:items-center sm:justify-between">
        <p className="text-[12px] text-[#9aa0aa]" data-testid="livestream-new-hint">
          {selected
            ? `Canale selezionato: ${selected.username}`
            : "Seleziona un canale con OAuth pronto e live streaming abilitato per continuare."}
        </p>
        <div className="flex shrink-0 items-center gap-2">
          <Link
            to="/app/livestreams"
            className="rounded-lg border border-white/[0.10] px-3.5 py-2 text-[12px] font-semibold text-[#cdd2da] no-underline hover:bg-white/[0.06] transition-colors"
          >
            Annulla
          </Link>
          <button
            type="button"
            onClick={onContinue}
            disabled={!selected}
            title={selected ? undefined : "Serve un canale con live streaming abilitato"}
            className="inline-flex items-center gap-2 rounded-lg bg-violet-500 px-4 py-2 text-[12px] font-bold text-white transition-colors hover:bg-violet-400 disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-[#6b7280]"
            data-testid="livestream-new-continue"
          >
            Continua
            <ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </div>
    </>
  );
}

function isChannelEligible(channel: LivestreamChannel): boolean {
  return channel.oauth_ready && channel.live_enabled;
}

function ChannelCard({
  channel,
  selected,
  onSelect,
}: {
  channel: LivestreamChannel;
  selected: boolean;
  onSelect?: () => void;
}) {
  const eligible = isChannelEligible(channel);
  const blockers: string[] = [];
  if (!channel.oauth_ready) blockers.push("OAuth assente");
  if (channel.oauth_ready && !channel.live_enabled) blockers.push("Live non abilitato");

  return (
    <article
      role="radio"
      aria-checked={selected}
      aria-disabled={!eligible}
      tabIndex={eligible ? 0 : -1}
      onClick={() => onSelect?.()}
      onKeyDown={(event) => {
        if (eligible && (event.key === "Enter" || event.key === " ")) {
          event.preventDefault();
          onSelect?.();
        }
      }}
      data-testid={`livestream-new-channel-${channel.platform_account_id}`}
      className={cn(
        "flex items-start gap-3.5 rounded-2xl border p-4 transition-colors",
        selected
          ? "border-violet-400/50 bg-violet-500/[0.08] ring-1 ring-violet-400/30"
          : "border-white/[0.08] bg-white/[0.025]",
        eligible ? "cursor-pointer hover:border-violet-400/30 hover:bg-white/[0.05]" : "opacity-70",
      )}
    >
      <div
        className={cn(
          "mt-0.5 grid h-5 w-5 shrink-0 place-items-center rounded-full border",
          selected ? "border-violet-400 bg-violet-500" : "border-white/25",
        )}
        aria-hidden="true"
      >
        {selected && <CheckCircle2 size={13} className="text-white" />}
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-2.5">
        <div className="flex min-w-0 items-center gap-3">
          <div
            className={cn(
              "grid h-10 w-10 shrink-0 place-items-center rounded-xl text-[15px] font-bold",
              eligible ? "bg-violet-500/15 text-violet-200" : "bg-white/[0.06] text-[#7f8591]",
            )}
          >
            {(channel.username || "C").slice(0, 1).toUpperCase()}
          </div>
          <div className="min-w-0">
            <p className="truncate text-[15px] font-semibold text-white" title={channel.username}>
              {channel.username || `Canale #${channel.platform_account_id}`}
            </p>
            <p className="truncate font-mono text-[11px] text-[#7f8591]" title={channel.platform_user_id}>
              {channel.platform_user_id}
            </p>
          </div>
          {!eligible && (
            <span className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-lg border border-amber-500/25 bg-amber-500/[0.08] px-2 py-1 text-[10px] font-semibold text-amber-200" title={blockers.join(", ")}>
              <AlertTriangle size={11} aria-hidden="true" />
              {blockers.join(" · ")}
            </span>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-1.5 pl-8 text-[11px]">
          <Badge
            tone={channel.oauth_ready ? "success" : "danger"}
            label={channel.oauth_ready ? "OAuth: pronto" : "OAuth: assente"}
            testid="livestream-new-oauth"
          />
          <Badge
            tone={channel.live_enabled ? "success" : "danger"}
            label={channel.live_enabled ? "Live: abilitato" : "Live: non abilitato"}
            testid="livestream-new-live"
          />
          <Badge tone="neutral" label={`Ultima verifica: ${formatLastVerified(channel.last_verified_at)}`} testid="livestream-new-verified" />
          <Badge tone="neutral" label={`Live attive: ${channel.active_lives}`} testid="livestream-new-active" />
        </div>
      </div>
    </article>
  );
}

function Badge({ tone, label, testid }: { tone: "success" | "danger" | "neutral"; label: string; testid: string }) {
  const Icon = tone === "success" ? ShieldCheck : tone === "danger" ? XCircle : null;
  return (
    <span
      data-testid={testid}
      className={cn(
        "inline-flex items-center gap-1 rounded-lg border px-2 py-1 font-medium",
        tone === "success" && "border-emerald-500/20 bg-emerald-500/[0.07] text-emerald-300",
        tone === "danger" && "border-red-500/20 bg-red-500/[0.07] text-red-300",
        tone === "neutral" && "border-white/[0.08] bg-white/[0.04] text-[#9aa0aa]",
      )}
    >
      {Icon && <Icon size={11} aria-hidden="true" />}
      {label}
    </span>
  );
}
