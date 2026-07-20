import { useEffect } from "react";

/**
 * Per-page SEO meta tags. Mounted at the top of a page component; updates
 * `document.title`, `document.head` meta tags, and the canonical link via
 * `useEffect`. Renders nothing (returns `null`).
 *
 * Why a custom component instead of `react-helmet`:
 *   - No external dependency; bundle stays smaller.
 *   - Pure DOM API; one effect, deterministic ordering, no Context
 *     threading.
 *   - Easy to grep for (one file, all logic visible).
 *
 * Page-level defaults shipped in `web/index.html` (Landing-targeted)
 * are overridden by this component on every subsequent page mount.
 * The Landing component still mounts <Seo /> so the canonical URL stays
 * `https://app.instaedit.org/` and the JSON-LD block in index.html
 * remains the canonical schema source.
 */
export type SeoProps = {
  /** Final document.title value. */
  title: string;
  /** `<meta name="description">` + OG/Twitter description. */
  description: string;
  /** Full absolute URL e.g. https://app.instaedit.org/editor */
  canonical: string;
  /** Absolute og:image URL. Defaults to the shared app-icon. */
  ogImage?: string;
  /** OG type; default "website". Pages can override to "article". */
  ogType?: "website" | "article";
  /** Set true to stop indexing on this route (e.g. /login). */
  noIndex?: boolean;
};

const DEFAULT_OG_IMAGE = "https://app.instaedit.org/app-icon-1024.png";

export function Seo({
  title,
  description,
  canonical,
  ogImage = DEFAULT_OG_IMAGE,
  ogType = "website",
  noIndex = false,
}: SeoProps) {
  useEffect(() => {
    setMeta("description", description, "name");
    setMeta("og:title", title, "property");
    setMeta("og:description", description, "property");
    setMeta("og:url", canonical, "property");
    setMeta("og:type", ogType, "property");
    setMeta("og:image", ogImage, "property");
    setMeta("twitter:card", "summary_large_image", "name");
    setMeta("twitter:title", title, "name");
    setMeta("twitter:description", description, "name");
    setMeta("twitter:url", canonical, "name");
    setMeta("twitter:image", ogImage, "name");
    setMeta(
      "robots",
      noIndex ? "noindex, nofollow" : "index, follow",
      "name",
    );

    let link = document.head.querySelector(
      'link[rel="canonical"]',
    ) as HTMLLinkElement | null;
    if (!link) {
      link = document.createElement("link");
      link.setAttribute("rel", "canonical");
      document.head.appendChild(link);
    }
    link.href = canonical;

    document.title = title;
  }, [title, description, canonical, ogImage, ogType, noIndex]);

  return null;
}

function setMeta(
  name: string,
  content: string,
  attr: "name" | "property",
): void {
  const selector = `meta[${attr}="${CSS.escape(name)}"]`;
  let el = document.head.querySelector(selector) as HTMLMetaElement | null;
  if (!el) {
    el = document.createElement("meta");
    el.setAttribute(attr, name);
    document.head.appendChild(el);
  }
  el.setAttribute("content", content);
}
