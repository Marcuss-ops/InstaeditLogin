/**
 * ChannelActions — the three distinct channel-termination commands.
 *
 * These commands are intentionally SEPARATE (account-lifecycle audit):
 * the trash icon inside a folder only removes a channel from the
 * folder, while this component owns the channel-level commands with
 * honest labels and explicit confirmations:
 *
 *   1. Disconnetti canale   → POST /api/v1/accounts/{id}/disconnect
 *      soft-disconnect. The row stays for audit; the shared Google
 *      grant is preserved while sibling channels still use it and
 *      the token is revoked only when the last channel disconnects.
 *   2. Elimina definitivamente → DELETE /api/v1/accounts/{id}/data
 *      permanent removal (hard-delete / tombstone). Wired here so
 *      the UI contract is in place; the endpoint is the backend P1
 *      step of the account-lifecycle plan.
 *   3. Revoca account Google e tutti i canali → DELETE
 *      /api/v1/accounts/{id}/oauth-grant  (YouTube only) — revokes
 *      the whole shared grant and disconnects every channel on it.
 *
 * The component owns confirmations + fetch and calls onDone() after
 * a successful action so the parent can reload or navigate away.
 * Errors surface through authedFetch's shared toast handling.
 */
import { useState } from "react";
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

  const run = async (
    action: Exclude<BusyAction, null>,
    method: string,
    endpoint: string,
    confirmMessage: string,
  ) => {
    if (!window.confirm(confirmMessage)) return;
    setBusy(action);
    try {
      await authedFetch(endpoint, { method });
      onDone();
    } catch {
      // authedFetch already toasts the server error; the tile stays so
      // the user can retry. Never leave an unhandled rejection.
    } finally {
      setBusy(null);
    }
  };

  const handle = account.username || `#${account.id}`;

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
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
          Il canale non sarà più utilizzabile. La cronologia viene conservata.
        </p>
      </button>

      {/* 2. Permanent delete — hard-delete / tombstone contract. */}
      <button
        type="button"
        onClick={() =>
          void run(
            "delete",
            "DELETE",
            `/api/v1/accounts/${account.id}/data`,
            `Eliminare definitivamente il canale ${handle}?\n\nQuesta azione rimuove il canale e i suoi dati da InstaEdit e non può essere annullata.`,
          )
        }
        disabled={busy !== null}
        className={cn(TILE_BASE, RED_TILE)}
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
          Rimuove definitivamente il canale e i suoi dati. Azione irreversibile.
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
              `Revocare l'account Google di ${handle} e disconnettere TUTTI i canali collegati?\n\nIl token Google verrà revocato e ogni canale che condivide questo collegamento smetterà di funzionare su InstaEdit.`,
            )
          }
          disabled={busy !== null}
          className={cn(TILE_BASE, RED_TILE)}
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
            Revoca il collegamento Google e disconnette tutti i canali che lo
            condividono.
          </p>
        </button>
      )}
    </div>
  );
}
