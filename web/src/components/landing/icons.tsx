import type { LogoProps } from "../brand/PlatformLogos";

export type { LogoProps } from "../brand/PlatformLogos";

export function IconSchedule({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      className={className}
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="1.7" />
      <path
        d="M12 7v5l3 2"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function IconAnalyze({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      className={className}
      aria-hidden="true"
    >
      <path
        d="M3.5 20V4M3.5 20h17"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
      <rect
        x="7"
        y="13"
        width="3"
        height="6"
        rx="0.6"
        fill="currentColor"
        opacity="0.55"
      />
      <rect
        x="12"
        y="9"
        width="3"
        height="10"
        rx="0.6"
        fill="currentColor"
        opacity="0.75"
      />
      <rect
        x="17"
        y="6"
        width="3"
        height="13"
        rx="0.6"
        fill="currentColor"
      />
    </svg>
  );
}
