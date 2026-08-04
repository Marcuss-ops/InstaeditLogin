/**
 * useThumbnailAutosave — debounced canvas snapshot autosave.
 *
 * Implements the Dark Editor autosave contract:
 *
 *   modifica → attesa 1–2s → snapshot → PUT …/snapshot → revisione
 *
 * Status is REAL, never guessed:
 *   - "saving"  → the PUT is in flight ("Salvataggio…")
 *   - "saved"   → the server persisted this exact snapshot
 *                 ("Salvato alle HH:MM" — never before the response)
 *   - "dirty"   → local changes exist, debounce pending
 *                 ("Modifiche non salvate")
 *   - "error"   → the PUT failed; the last snapshot was NOT persisted
 *                 ("Errore di salvataggio" + retry). A 409
 *                 PROJECT_VERSION_CONFLICT pauses autosave so the
 *                 editor can offer "Ricarica versione recente".
 *
 * The hook never reports "saved" for a snapshot the server has not
 * acked: the last-saved baseline advances only from the PUT response,
 * and `beforeunload` fires while local state diverges from it.
 *
 * The version returned by each save (`result.version`) becomes the
 * next `base_version`; the caller passes it back down as `version`.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  saveThumbnailSnapshot,
  THUMBNAIL_RENDERER_VERSION,
  toProjectVersionConflictError,
} from "../api/thumbnailProjectsApi";
import type {
  ProjectVersionConflict,
  ThumbnailCanvasSnapshot,
  ThumbnailProjectSnapshotResult,
} from "../types";

export type ThumbnailSaveStatus = "idle" | "dirty" | "saving" | "saved" | "error";

export interface ThumbnailAutosaveResult {
  status: ThumbnailSaveStatus;
  lastSavedAt: Date | null;
  /** snapshot_sha256 hex returned by the last successful save. */
  lastHash: string | null;
  error: string | null;
  conflict: ProjectVersionConflict | null;
  /** Cancel the debounce and save immediately; true when persisted. */
  flush: () => Promise<boolean>;
  /** Re-attempt the last failed save. */
  retry: () => void;
  /**
   * Re-baseline after the editor reloaded server truth (e.g. after a
   * conflict "Ricarica versione recente"): stores snapshot/version as
   * the last-saved state and clears any conflict pause.
   */
  reset: (snapshot: ThumbnailCanvasSnapshot, version: number) => void;
}

export interface UseThumbnailAutosaveOptions {
  workspaceId: number;
  projectId: string;
  /** The live editor snapshot; any JSON change marks the state dirty. */
  snapshot: ThumbnailCanvasSnapshot;
  /** Current server version — the base_version for the next save. */
  version: number;
  /** False while the project is loading or after a conflict pause. */
  enabled: boolean;
  debounceMs?: number;
  onSaved?: (result: ThumbnailProjectSnapshotResult) => void;
}

export function useThumbnailAutosave({
  workspaceId,
  projectId,
  snapshot,
  version,
  enabled,
  debounceMs = 1500,
  onSaved,
}: UseThumbnailAutosaveOptions): ThumbnailAutosaveResult {
  const [status, setStatus] = useState<ThumbnailSaveStatus>("idle");
  const [lastSavedAt, setLastSavedAt] = useState<Date | null>(null);
  const [lastHash, setLastHash] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [conflict, setConflict] = useState<ProjectVersionConflict | null>(null);

  // Latest values live in refs so debounced closures never read stale
  // snapshot/version state.
  const latestRef = useRef({ snapshot, version, snapshotJson: JSON.stringify(snapshot) });
  latestRef.current = { snapshot, version, snapshotJson: JSON.stringify(snapshot) };

  const lastSavedJsonRef = useRef<string | null>(null);
  const lastSavedVersionRef = useRef<number>(0);
  const timerRef = useRef<number | null>(null);
  const inFlightRef = useRef(false);
  const conflictRef = useRef(false);
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;
  // True once the hook unmounts; prevents the post-unmount failure path
  // from re-scheduling saves on a component that is no longer mounted.
  const unmountedRef = useRef(false);

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const scheduleSave = useCallback(() => {
    if (!enabledRef.current || conflictRef.current) return;
    setStatus((prev) => (prev === "saved" || prev === "idle" ? "dirty" : prev));
    clearTimer();
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      void doSaveRef.current();
    }, debounceMs);
  }, [debounceMs, clearTimer]);

  const doSave = useCallback(async (): Promise<boolean> => {
    if (!enabledRef.current || conflictRef.current) return false;
    const { snapshotJson, snapshot: snap, version: baseVersion } = latestRef.current;
    if (lastSavedJsonRef.current !== null && lastSavedJsonRef.current === snapshotJson) {
      return false; // nothing to persist — never saves unchanged snapshots
    }
    if (inFlightRef.current) {
      scheduleSave();
      return false;
    }
    inFlightRef.current = true;
    setStatus("saving");
    setError(null);
    try {
      const result = await saveThumbnailSnapshot(workspaceId, projectId, {
        schema_version: 1,
        snapshot: snap,
        renderer_version: THUMBNAIL_RENDERER_VERSION,
        base_version: baseVersion,
      });
      // Advance the baseline ONLY from the server ack.
      lastSavedJsonRef.current = snapshotJson;
      lastSavedVersionRef.current = result.version;
      setStatus("saved");
      setLastSavedAt(new Date());
      setLastHash(result.snapshot_sha256);
      onSaved?.(result);
      return true;
    } catch (err) {
      const conflictErr = toProjectVersionConflictError(err);
      if (conflictErr) {
        conflictRef.current = true;
        setConflict({
          code: "PROJECT_VERSION_CONFLICT",
          ...(conflictErr.currentVersion === undefined
            ? {}
            : { current_version: conflictErr.currentVersion }),
        });
        setError("Conflitto di versione: il progetto è stato modificato altrove.");
      } else {
        setError(err instanceof Error ? err.message : "Errore di salvataggio.");
      }
      setStatus("error");
      return false;
    } finally {
      inFlightRef.current = false;
      // Edits made while the PUT was in flight must not be lost: the
      // latest snapshot differs from the baseline → schedule again.
      // (Skipped after unmount — the component is gone; the unmount
      // cleanup already flushed the pending debounce fire-and-forget.)
      if (!unmountedRef.current) {
        const current = latestRef.current.snapshotJson;
        if (current !== lastSavedJsonRef.current && !conflictRef.current) {
          scheduleSave();
        }
      }
    }
  }, [workspaceId, projectId, onSaved, scheduleSave]);

  const doSaveRef = useRef(doSave);
  doSaveRef.current = doSave;

  // React to snapshot changes: any JSON divergence from the baseline is
  // unsaved work → schedule the debounced save.
  useEffect(() => {
    const { snapshotJson } = latestRef.current;
    if (!enabled) return;
    if (lastSavedJsonRef.current === null) {
      // First observation of the project — adopt it as the baseline so
      // a freshly loaded snapshot is never re-saved as if it were an
      // edit ("mai falso 'Salvato'", and also no spurious first save).
      lastSavedJsonRef.current = snapshotJson;
      lastSavedVersionRef.current = latestRef.current.version;
      return;
    }
    if (lastSavedJsonRef.current !== snapshotJson) {
      scheduleSave();
    }
  }, [latestRef.current.snapshotJson, enabled, scheduleSave]);

  // beforeunload warns while unsaved edits exist; on unmount the pending
  // debounce is flushed fire-and-forget so SPA navigation (e.g. back to
  // the Copertine library) never loses edits still inside the debounce
  // window (DoD: flush prima di chiudere l'editor). The fetch survives
  // the unmount; doSave early-returns when there is nothing to persist.
  useEffect(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      const { snapshotJson } = latestRef.current;
      if (lastSavedJsonRef.current !== null && lastSavedJsonRef.current !== snapshotJson) {
        event.preventDefault();
        event.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => {
      window.removeEventListener("beforeunload", handleBeforeUnload);
      unmountedRef.current = true;
      clearTimer();
      // Fire-and-forget the pending debounced save if anything is dirty.
      if (
        enabledRef.current &&
        !conflictRef.current &&
        lastSavedJsonRef.current !== null &&
        lastSavedJsonRef.current !== latestRef.current.snapshotJson
      ) {
        void doSaveRef.current();
      }
    };
  }, [clearTimer]);

  const flush = useCallback(async (): Promise<boolean> => {
    clearTimer();
    return doSaveRef.current();
  }, [clearTimer]);

  const retry = useCallback(() => {
    void doSaveRef.current();
  }, []);

  const reset = useCallback((s: ThumbnailCanvasSnapshot, v: number) => {
    lastSavedJsonRef.current = JSON.stringify(s);
    lastSavedVersionRef.current = v;
    conflictRef.current = false;
    setConflict(null);
    setError(null);
    setStatus("idle");
    clearTimer();
  }, [clearTimer]);

  return { status, lastSavedAt, lastHash, error, conflict, flush, retry, reset };
}
