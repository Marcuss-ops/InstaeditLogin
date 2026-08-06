/**
 * @deprecated Kept as a compatibility export for older landing consumers.
 * Functional icons use `IconProps`; provider logos use the brand catalog.
 */
export type { LogoProps } from "../brand/PlatformLogos";

export type RowPlatform =
  | "instagram"
  | "tiktok"
  | "youtube"
  | "facebook"
  | "x"
  | "linkedin"
  | "threads";

export type MockupRow = {
  thumb: string;
  title: string;
  meta: string;
  time: string;
  badges: ReadonlyArray<RowPlatform>;
};
