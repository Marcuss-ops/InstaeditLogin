/**
 * Per-group accent colors for the Groups page. Every group folder gets a
 * stable color derived from its name (hash → palette index) so the same
 * folder always renders with the same accent everywhere — tray badges,
 * group chips and the detail-panel header — while different folders stay
 * visually distinguishable at a glance.
 *
 * Colors are tuned for the dark UI: `text` is the vivid accent hex and
 * `bg` is the same hue at ~12% alpha, used as a translucent chip
 * background.
 *
 * Note: "unico" is best-effort — the 12-color palette plus a name hash
 * means two distinct names CAN collide onto the same accent. That is a
 * deliberate trade-off: colors stay stable across sessions and devices
 * without persisting an assignment table. For realistic folder counts
 * collisions are rare and harmless (the group name is always visible
 * next to the color).
 */
export const GROUP_ACCENT_COLORS = [
  "#fbbf24", // amber
  "#a78bfa", // violet
  "#34d399", // emerald
  "#60a5fa", // blue
  "#fb7185", // rose
  "#2dd4bf", // teal
  "#facc15", // yellow
  "#e879f9", // fuchsia
  "#4ade80", // green
  "#fb923c", // orange
  "#38bdf8", // sky
  "#818cf8", // indigo
] as const;

export type GroupAccent = { text: string; bg: string };

function hashString(input: string): number {
  // FNV-1a — deterministic across renders, sessions and environments.
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

export function groupAccent(groupName: string): GroupAccent {
  const text = GROUP_ACCENT_COLORS[hashString(groupName) % GROUP_ACCENT_COLORS.length];
  // 0x1f ≈ 12% alpha, a translucent chip background for the dark theme.
  return { text, bg: `${text}1f` };
}
