import type { SVGProps } from "react";

export type LogoProps = SVGProps<SVGSVGElement> & { className?: string };

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
