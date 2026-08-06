import type { LucideIcon, LucideProps } from "lucide-react";
import {
  AlertCircle,
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  BarChart3,
  Calendar,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  FolderTree,
  ImageIcon,
  Info,
  LayoutDashboard,
  Link2,
  Loader2,
  LogOut,
  Menu,
  Pencil,
  Plus,
  Radio,
  RefreshCw,
  Save,
  Search,
  Shield,
  Trash2,
  Upload,
  Video,
  X,
} from "lucide-react";

/**
 * Canonical functional icon catalog.
 *
 * This catalog contains Lucide UI symbols only: actions, navigation, status,
 * and product concepts. Provider trademarks belong exclusively to
 * `../brand/PlatformLogos` and must not be added here.
 */
export const FUNCTIONAL_ICON_CATALOG = {
  navigation: {
    back: ArrowLeft,
    forward: ArrowRight,
    menu: Menu,
    close: X,
    expand: ChevronDown,
    collapse: ChevronLeft,
    next: ChevronRight,
  },
  actions: {
    add: Plus,
    edit: Pencil,
    save: Save,
    delete: Trash2,
    refresh: RefreshCw,
    search: Search,
    upload: Upload,
    externalLink: ExternalLink,
    logout: LogOut,
  },
  status: {
    success: CheckCircle2,
    warning: AlertTriangle,
    error: AlertCircle,
    info: Info,
    loading: Loader2,
  },
  product: {
    calendar: Calendar,
    analytics: BarChart3,
    dashboard: LayoutDashboard,
    video: Video,
    image: ImageIcon,
    folder: FolderTree,
    link: Link2,
    live: Radio,
    admin: Shield,
  },
} as const satisfies Record<string, Record<string, LucideIcon>>;

export type FunctionalIconGroup = keyof typeof FUNCTIONAL_ICON_CATALOG;
export type FunctionalIconName = {
  [Group in FunctionalIconGroup]: keyof (typeof FUNCTIONAL_ICON_CATALOG)[Group];
}[FunctionalIconGroup];

export type FunctionalIconNameForGroup<Group extends FunctionalIconGroup> = keyof
  (typeof FUNCTIONAL_ICON_CATALOG)[Group];

export type FunctionalIconProps = {
  [Group in FunctionalIconGroup]: {
    group: Group;
    name: FunctionalIconNameForGroup<Group>;
  };
}[FunctionalIconGroup] & LucideProps;

/** Internal read-only lookup used after the catalog key has been validated. */
function resolveFunctionalIcon(
  group: FunctionalIconGroup,
  name: FunctionalIconName,
): LucideIcon | undefined {
  const icons = FUNCTIONAL_ICON_CATALOG[group] as Record<string, LucideIcon>;
  return icons[name];
}

/** Resolve a shared functional icon with a group/name relationship at compile time. */
export function getFunctionalIcon<Group extends FunctionalIconGroup>(
  group: Group,
  name: FunctionalIconNameForGroup<Group>,
): LucideIcon | undefined {
  return resolveFunctionalIcon(group, name as FunctionalIconName);
}

/** Render a catalogued functional icon by a type-safe semantic group/name. */
export function FunctionalIcon({ group, name, ...props }: FunctionalIconProps) {
  // FunctionalIconProps preserves the group/name relationship for callers;
  // the catalog lookup itself is intentionally read-only and indexed by the
  // already validated semantic name union.
  const Icon = resolveFunctionalIcon(group, name);
  if (!Icon) return null;
  return <Icon {...props} />;
}
