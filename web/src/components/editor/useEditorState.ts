import { useState } from "react";
import { SHORT_DEMOS, LONGFORM_DEMOS } from "./shared";
import { PLATFORM_REGISTRY } from "../brand/PlatformLogos";

export interface EditorState {
  /** Demo / marketing data used by the editor page. */
  shortDemos: typeof SHORT_DEMOS;
  longFormDemos: typeof LONGFORM_DEMOS;
  platformRegistry: typeof PLATFORM_REGISTRY;
}

export function useEditorState(): EditorState {
  const [state] = useState<EditorState>({
    shortDemos: SHORT_DEMOS,
    longFormDemos: LONGFORM_DEMOS,
    platformRegistry: PLATFORM_REGISTRY,
  });
  return state;
}
