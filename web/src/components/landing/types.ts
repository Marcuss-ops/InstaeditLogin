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
