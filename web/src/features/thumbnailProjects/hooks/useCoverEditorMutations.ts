/**
 * useCoverEditorMutations — snapshot mutation helpers for the Cover
 * editor page.
 *
 * Pure move of the page's inline object mutations (update / add /
 * remove / duplicate / reorder / background). Same closures over the
 * page's snapshot state, same behavior — extracted only to keep
 * CoverEditor.tsx under the LOC threshold. The page passes the live
 * snapshot, setters, and selection; every helper no-ops on null.
 */
import { type Dispatch, type SetStateAction } from "react";
import { makeId } from "../editor/objects";
import type { EditorSnapshot } from "../editor/snapshot";
import type { ThumbnailSnapshotObject } from "../types";

interface UseCoverEditorMutationsParams {
  snapshot: EditorSnapshot | null;
  setSnapshot: Dispatch<SetStateAction<EditorSnapshot | null>>;
  selectedId: string | null;
  setSelectedId: (id: string | null) => void;
}

export function useCoverEditorMutations({
  snapshot,
  setSnapshot,
  selectedId,
  setSelectedId,
}: UseCoverEditorMutationsParams) {
  const updateObject = (id: string, patch: Partial<ThumbnailSnapshotObject>) => {
    if (!snapshot) return;
    setSnapshot({
      ...snapshot,
      objects: snapshot.objects.map((obj) => (obj.id === id ? { ...obj, ...patch } : obj)),
    });
  };

  const addObject = (obj: ThumbnailSnapshotObject) => {
    if (!snapshot) return;
    setSnapshot({ ...snapshot, objects: [...snapshot.objects, obj] });
    setSelectedId(obj.id);
  };

  const removeSelected = () => {
    if (!snapshot || !selectedId) return;
    setSnapshot({ ...snapshot, objects: snapshot.objects.filter((o) => o.id !== selectedId) });
    setSelectedId(null);
  };

  const duplicateSelected = () => {
    if (!snapshot || !selectedId) return;
    const source = snapshot.objects.find((o) => o.id === selectedId);
    if (!source) return;
    const copy: ThumbnailSnapshotObject = {
      ...source,
      id: makeId(source.type),
      x: (source.x ?? 0) + 24,
      y: (source.y ?? 0) + 24,
    };
    addObject(copy);
  };

  const reorder = (id: string, direction: -1 | 1) => {
    if (!snapshot) return;
    const index = snapshot.objects.findIndex((o) => o.id === id);
    const target = index + direction;
    if (index < 0 || target < 0 || target >= snapshot.objects.length) return;
    const objects = [...snapshot.objects];
    const [obj] = objects.splice(index, 1);
    objects.splice(target, 0, obj!);
    setSnapshot({ ...snapshot, objects });
  };

  const setBackground = (background: string) => {
    if (!snapshot) return;
    setSnapshot({ ...snapshot, canvas: { ...snapshot.canvas, background } });
  };

  return { updateObject, addObject, removeSelected, duplicateSelected, reorder, setBackground };
}
