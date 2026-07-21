import type { LogoProps } from "./types";

export function InstagramLogo({
  className = "w-6 h-6",
  ...rest
}: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="5" fill="#E4405F" />
      <circle cx="12" cy="12" r="4.2" stroke="#fff" strokeWidth="1.6" />
      <circle cx="17.4" cy="6.6" r="0.95" fill="#fff" />
    </svg>
  );
}

export function FacebookLogo({
  className = "w-6 h-6",
  ...rest
}: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="4" fill="#1877F2" />
      <path
        d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z"
        fill="#fff"
      />
    </svg>
  );
}

export function YouTubeLogo({
  className = "w-6 h-6",
  ...rest
}: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="5" width="20" height="14" rx="3.5" fill="#FF0000" />
      <path d="M10 9.2v5.6l4.4-2.8L10 9.2z" fill="#fff" />
    </svg>
  );
}

export function TikTokLogo({
  className = "w-6 h-6",
  ...rest
}: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="4.5" fill="#000" />
      <path
        d="M15.6 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45"
        stroke="#25F4EE"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
      <path
        d="M15.85 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45"
        stroke="#FE2C55"
        strokeWidth="1.7"
        strokeLinecap="round"
        transform="translate(0.5 -0.4)"
      />
      <path
        d="M15.6 4.5a4.2 4.2 0 0 0 4.2 4.2"
        stroke="#25F4EE"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function XLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect width="24" height="24" rx="4" fill="#fff" />
      <path
        d="M14.65 11l4.05-5h-1.55l-3.45 4.34L10.85 6h-4.4l4.5 7.5L6 19h1.55l3.8-4.74L14.6 19h4l-4.65-8h.7zm-2 7l-.5-.7L7.85 7h1.4l4.4 6.3 1.95 2.7.5.7-3.45 0z"
        fill="#000"
      />
    </svg>
  );
}

export function LinkedInLogo({
  className = "w-6 h-6",
  ...rest
}: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="3" fill="#0A66C2" />
      <circle cx="7" cy="8" r="1.15" fill="#fff" />
      <rect x="6.05" y="10" width="2.1" height="6.5" rx="0.3" fill="#fff" />
      <path
        d="M10 16.5v-6.5h2v1.1c.45-.7 1.3-1.3 2.4-1.3 1.7 0 2.6 1.1 2.6 3V16.5h-2v-3.4c0-.9-.4-1.5-1.2-1.5s-1.2.5-1.2 1.5V16.5H10z"
        fill="#fff"
      />
    </svg>
  );
}

export function ThreadsLogo({
  className = "w-6 h-6",
  ...rest
}: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect width="24" height="24" rx="6" fill="#000" />
      <path
        d="M12 6.5c2.7 0 4.7 1.6 4.7 4.7s-2 4.7-4.7 4.7-4.7-1.6-4.7-4.7"
        stroke="#fff"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
      <path
        d="M12 6.5c-3 0-5 2-5 5s2 5 5 5"
        stroke="#fff"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

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
