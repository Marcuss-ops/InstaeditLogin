/**
 * ChannelActions — the three distinct channel-termination commands.
 *
 * These commands are intentionally SEPARATE (account-lifecycle audit):
 * the trash icon inside a folder only removes a channel from the
 * folder, while this component owns the channel-level commands with
 * honest labels and explicit confirmations:
 *
 *   1. Disconnetti canale → POST /api/v1/accounts/{id}/disconnect
 *      soft-disconnect. The row stays for audit; the shared Google
 *      grant is preserved while sibling channels still use it and
 *      the token is revoked only when the last channel disconnects.
 *   2. Elimina definitivamente → DELETE /api/v1/accounts/{id}/data
 *      permanent removal (hard-delete / tombstone), requiring the exact
 *      channel-name confirmation before the JSON request is sent.
 *   3. Revoca account Google e tutti i canali → DELETE
 *      /api/v1/accounts/{id}/oauth-grant  (YouTube only) — revokes
 *      the whole shared grant and disconnects every channel on it.
 *
 * The component owns confirmations + fetch and calls onDone() after
 * a successful action so the parent can reload or navigate away.
 * Errors surface through authedFetch's shared toast handling.
 */
import { useRef, useState } from "react";
import { RefreshCw, ShieldX, Unplug, XCircle } from "lucide-react";
import { authedFetch } from "../../../lib/auth";
import { cn } from "../../../lib/utils";

export interface ChannelActionsAccount {
  id: number;
  platform: string;
  username: string;
}

interface ChannelActionsProps {
  account: ChannelActionsAccount;
  /** Invoked after a successful action so the parent reloads or navigates. */
  onDone: () => void;
}

type BusyAction = "disconnect" | "delete" | "revoke-grant" | null;

const TILE_BASE =
  "text-left p-4 rounded-xl border transition-colors disabled:opacity-60 disabled:cursor-progress";

const AMBER_TILE =
  "bg-amber-500/[0.08] border-amber-500/30 hover:bg-amber-500/[0.16] hover:border-amber-500/50";

const RED_TILE =
  "bg-red-500/[0.08] border-red-500/30 hover:bg-red-500/[0.16] hover:border-red-500/50";

export function ChannelActions({ account, onDone }: ChannelActionsProps) {
  const [busy, setBusy] = useState<BusyAction>(null);
  // State updates are asynchronous: two clicks in the same event turn can
  // otherwise both pass the disabled check before React re-renders. Keep a
  // synchronous guard as well so a double-click can never submit two lifecycle
  // mutations.
  const busyRef = useRef<BusyAction>(null);

  const run = async (
    action: Exclude<BusyAction, null>,
    method: string,
    endpoint: string,
    confirmMessage: string,
  ) => {
    if (busyRef.current !== null) return;
    if (!window.confirm(confirmMessage)) return;
    busyRef.current = action;
    setBusy(action);
    try {
      await authedFetch(endpoint, { method });
      onDone();
    } catch {
      // authedFetch already toasts the server error; the tile stays so
      // the user can retry. Never leave an unhandled rejection.
    } finally {
      if (busyRef.current === action) busyRef.current = null;
      setBusy(null);
    }
  };

  const runPermanentDelete = async () => {
    const confirmation = window.prompt(
      `Per eliminare definitivamente ${handle}, digita esattamente:\n${handle}`,
      "",
    );
    if (confirmation !== handle || busyRef.current !== null) return;
    busyRef.current = "delete";
    setBusy("delete");
    try {
      await authedFetch(`/api/v1/accounts/${account.id}/data`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ confirmation }),
      });
      onDone();
    } catch {
      // authedFetch already toasts the server error; keep the action retryable.
    } finally {
      if (busyRef.current === "delete") busyRef.current = null;
      setBusy(null);
    }
  };

  const handle = account.username || `#${account.id}`;

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3" data-testid="channel-actions">
      {/* 1. Soft disconnect — the row stays, siblings unaffected. */}
      <button
        type="button"
        onClick={() =>
          void run(
            "disconnect",
            "POST",
            `/api/v1/accounts/${account.id}/disconnect`,
            `Disconnetti il canale ${handle}?\n\nIl canale non sarà più utilizzabile da InstaEdit.\nLa cronologia verrà conservata.\nGli altri canali dello stesso account Google non saranno interessati.`,
          )
        }
        disabled={busy !== null}
        className={cn(TILE_BASE, AMBER_TILE)}
        data-testid="channel-action-disconnect"
        title="Blocca questo canale senza eliminare lo storico"
      >
        <div className="flex items-center gap-2 mb-1">
          <Unplug size={16} className="text-amber-300" aria-hidden="true" />
          <span className="text-[14px] font-bold text-amber-200">
            Disconnetti canale
          </span>
          {busy === "disconnect" && (
            <RefreshCw size={12} className="animate-spin text-amber-200/70 ml-auto" aria-hidden="true" />
          )}
        </div>
        <p className="text-[12px] text-amber-100/60 leading-snug">
          Blocca solo questo canale. Lo storico resta disponibile e gli altri canali non cambiano.
        </p>
      </button>

      {/* 2. Permanent delete — hard-delete / tombstone contract. */}
      <button
        type="button"
        onClick={() => void runPermanentDelete()}
        disabled={busy !== null}
        className={cn(TILE_BASE, RED_TILE)}
        data-testid="channel-action-delete"
        title="Elimina definitivamente i dati di questo canale"
      >
        <div className="flex items-center gap-2 mb-1">
          <XCircle size={16} className="text-red-300" aria-hidden="true" />
          <span className="text-[14px] font-bold text-red-200">
            Elimina definitivamente
          </span>
          {busy === "delete" && (
            <RefreshCw size={12} className="animate-spin text-red-200/70 ml-auto" aria-hidden="true" />
          )}
        </div>
        <p className="text-[12px] text-red-100/60 leading-snug">
          Elimina solo i dati di questo canale. Azione irreversibile; richiede il nome esatto.
        </p>
      </button>

      {/* 3. Shared-grant revoke — YouTube only (the backend contract is
          YouTube-scoped and revokes the whole Google connection). */}
      {account.platform === "youtube" && (
        <button
          type="button"
          onClick={() =>
            void run(
              "revoke-grant",
              "DELETE",
              `/api/v1/accounts/${account.id}/oauth-grant`,
              `Revocare l'account Google di ${handle} e disconnettere TUTTI i canali collegati?\n\nIl grant Google verrà revocato. Tutti i canali che condividono questo collegamento smetteranno di funzionare su InstaEdit.`,
            )
          }
          disabled={busy !== null}
          className={cn(TILE_BASE, RED_TILE)}
          data-testid="channel-action-revoke-grant"
          title="Revoca l'account Google e tutti i canali collegati"
        >
          <div className="flex items-center gap-2 mb-1">
            <ShieldX size={16} className="text-red-300" aria-hidden="true" />
            <span className="text-[14px] font-bold text-red-200">
              Revoca account Google e tutti i canali
            </span>
            {busy === "revoke-grant" && (
              <RefreshCw size={12} className="animate-spin text-red-200/70 ml-auto" aria-hidden="true" />
            )}
          </div>
          <p className="text-[12px] text-red-100/60 leading-snug">
            Chiude il grant Google: tutti i canali collegati vengono disconnessi e le credenziali locali vengono revocate.
          </p>
        </button>
      )}
    </div>
  );
}
