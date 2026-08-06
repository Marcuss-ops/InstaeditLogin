import type { SVGProps } from "react";

/**
 * Props for custom functional UI icons.
 *
 * This type intentionally lives outside the brand catalog: provider logos use
 * `LogoProps` from `../brand/PlatformLogos`, while functional symbols use
 * `IconProps` or native `lucide-react` props.
 */
export type IconProps = SVGProps<SVGSVGElement> & { className?: string };

/**
 * @deprecated Use `IconProps` for functional icons. Import `LogoProps`
 * directly from `../brand/PlatformLogos` for provider artwork.
 */
export type { LogoProps } from "../brand/PlatformLogos";

/**
 * Small custom functional icons used by the landing page.
 *
 * The shared Lucide catalog lives in `../icons/FunctionalIcons`. This module
 * remains the home for landing-specific artwork that is not a provider logo.
 */
export function IconSchedule({ className = "w-5 h-5", ...rest }: IconProps) {
  const decorative = rest["aria-label"] == null && rest["aria-labelledby"] == null;
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      className={className}
      {...(decorative ? { "aria-hidden": true } : {})}
      {...rest}
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

export function IconAnalyze({ className = "w-5 h-5", ...rest }: IconProps) {
  const decorative = rest["aria-label"] == null && rest["aria-labelledby"] == null;
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      className={className}
      {...(decorative ? { "aria-hidden": true } : {})}
      {...rest}
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
