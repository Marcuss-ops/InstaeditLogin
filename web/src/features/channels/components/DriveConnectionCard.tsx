import { CheckCircle2, HardDrive, RefreshCw } from "lucide-react";
import type { ChannelAccount } from "../types";

interface DriveConnectionCardProps {
  accountState: ChannelAccount["account_state"];
  reconnectHref: string;
}

/**
 * Drive accounts do not have channel videos or privacy filters. Their
 * actionable state is the OAuth connection itself: when the grant is stale,
 * send the operator through the canonical reconnect flow so the backend can
 * exchange and persist a fresh token.
 */
export function DriveConnectionCard({
  accountState,
  reconnectHref,
}: DriveConnectionCardProps) {
  const reconnectRequired =
    accountState === "reconnect_required" || accountState == null;

  return (
    <section
      className="rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-6"
      data-testid="drive-connection-card"
    >
      <div className="flex items-start gap-4">
        <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-emerald-500/[0.12] text-emerald-300">
          <HardDrive size={21} aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <h2 className="text-[15px] font-bold text-white">
            Collegamento Google Drive
          </h2>
          {reconnectRequired ? (
            <>
              <p className="mt-1 text-[13px] text-[#9aa0aa]">
                Il collegamento non è più valido. Accedi di nuovo a Google per
                aggiornare il token e riprendere gli upload.
              </p>
              <a
                href={reconnectHref}
                className="mt-4 inline-flex items-center gap-1.5 rounded-xl bg-emerald-300 px-4 py-2 text-[13px] font-semibold text-black transition-colors hover:bg-emerald-200"
                data-testid="drive-reconnect"
              >
                <RefreshCw size={14} aria-hidden="true" />
                Ricollega Google Drive
              </a>
            </>
          ) : (
            <div className="mt-2 inline-flex items-center gap-1.5 text-[13px] text-emerald-300">
              <CheckCircle2 size={15} aria-hidden="true" />
              Google Drive collegato e pronto per gli upload
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
