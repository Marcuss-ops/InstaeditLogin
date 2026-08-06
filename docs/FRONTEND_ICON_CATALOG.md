# Frontend icon catalogs

The frontend uses two intentionally separate icon systems. Do not add a provider trademark to the functional catalog, and do not use a brand logo to represent a generic UI action.

## 1. Brand logos — provider identity

**Canonical module:** `web/src/components/brand/PlatformLogos.tsx`

Use this module for third-party provider identity:

- Instagram, Facebook, Threads, TikTok, X/Twitter, YouTube, LinkedIn, and Google Drive logos
- canonical provider IDs and legacy aliases
- provider names, brand colors, gradients, and marketing metadata
- `PlatformLogo`, `ProviderBadge`, `getProviderRegistryEntry`, and `PROVIDER_REGISTRY`

`PROVIDER_REGISTRY` is the source of truth. New provider artwork or provider metadata belongs there, not in a page, landing component, or Lucide catalog.

## 2. Functional icons — product actions and UI meaning

**Canonical module:** `web/src/components/icons/FunctionalIcons.tsx`

Use this module for shared Lucide symbols representing navigation, actions, status, and product concepts. Resolve semantic icons with `getFunctionalIcon` or render them with `FunctionalIcon`:

```tsx
import { FunctionalIcon } from "../../components/icons/FunctionalIcons";

<FunctionalIcon group="status" name="success" className="h-4 w-4" />
```

The catalog groups are:

- `navigation`: back, forward, menu, close, expand, collapse, next
- `actions`: add, edit, save, delete, refresh, search, upload, external link
- `status`: success, warning, error, info, loading
- `product`: calendar, analytics, video, image, folder, link, live

For a one-off icon that has no shared semantic need, importing the specific icon directly from `lucide-react` is acceptable. Add it to `FunctionalIcons.tsx` only when it is reused or needs a stable semantic name.

Landing-only custom functional SVGs remain in `web/src/components/landing/icons.tsx` and use the independent `IconProps` type. They are not brand logos and do not import types from the brand catalog. Its `LogoProps` re-export is retained only as a deprecated compatibility path for older consumers; new code must import `IconProps` or the brand type from its owning module.

## Naming and review rules

1. Provider trademarks always use `PlatformLogo`/`ProviderBadge` or a component derived from `PROVIDER_REGISTRY`.
2. Generic UI symbols use Lucide (`LucideProps`) or `FunctionalIcon`.
3. Do not create local provider SVGs in pages or feature components.
4. Do not put Lucide icons or generic action symbols into `PROVIDER_REGISTRY`.
5. Keep the two catalogs' tests next to their owning modules.
