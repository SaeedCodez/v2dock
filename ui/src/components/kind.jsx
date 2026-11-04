import {
  Cloud,
  Container,
  Database,
  FolderOpen,
  HardDrive,
  Vault,
  Workflow,
} from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Every kind of thing the app manages has one icon and one hue, used wherever
 * it appears (navigation, cards, lists), so colour tells you what you are
 * looking at before you read it. Status colours are never used here.
 */
export const KIND = {
  job: { icon: Workflow, tone: "violet" },
  repository: { icon: Vault, tone: "cyan" },
  folder: { icon: FolderOpen, tone: "fuchsia" },
  database: { icon: Database, tone: "fuchsia" },
  site: { icon: Container, tone: "fuchsia" },
};

export const TONE_TILE = {
  neutral: "bg-secondary text-muted-foreground",
  brand: "bg-primary/12 text-primary",
  violet: "bg-violet/12 text-violet",
  cyan: "bg-cyan/14 text-cyan",
  fuchsia: "bg-fuchsia/12 text-fuchsia",
  success: "bg-success/12 text-success",
  warning: "bg-warning/15 text-warning",
  destructive: "bg-destructive/12 text-destructive",
};

export const TONE_TEXT = {
  neutral: "text-muted-foreground",
  brand: "text-primary",
  violet: "text-violet",
  cyan: "text-cyan",
  fuchsia: "text-fuchsia",
  success: "text-success",
  warning: "text-warning",
  destructive: "text-destructive",
};

const TILE_SIZE = {
  sm: "size-8 rounded-lg [&_svg]:size-4",
  md: "size-10 rounded-xl [&_svg]:size-5",
  lg: "size-12 rounded-[14px] [&_svg]:size-6",
};

/** IconTile is the rounded, tinted square an icon sits in on cards and rows. */
export function IconTile({ icon: Icon, tone = "neutral", size = "md", className }) {
  return (
    <span
      className={cn(
        "grid shrink-0 place-items-center",
        TILE_SIZE[size],
        TONE_TILE[tone],
        className,
      )}
      aria-hidden="true"
    >
      {Icon ? <Icon /> : null}
    </span>
  );
}

/** The icon for a repository follows its backend: a cloud for S3, a disk otherwise. */
export function repositoryIcon(repo) {
  return repo?.backendType === "S3" ? Cloud : HardDrive;
}

/** KindTile renders a kind's own icon and hue, e.g. <KindTile kind="job" />. */
export function KindTile({ kind, icon, size = "md", className }) {
  const k = KIND[kind] || {};
  return <IconTile icon={icon || k.icon} tone={k.tone} size={size} className={className} />;
}
