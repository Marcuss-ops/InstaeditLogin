import { useState } from "react";
import { ChevronDown, ChevronRight, Folder } from "lucide-react";
import { cn } from "../../lib/utils";
import type { TreeNode } from "./groupsTypes";

export function TreeView({
  nodes,
  selectedGroupId,
  onSelect,
  depth = 0,
}: {
  nodes: TreeNode[];
  selectedGroupId: number | null;
  onSelect: (id: number) => void;
  depth?: number;
}) {
  return (
    <ul className="space-y-1">
      {nodes.map((n) => (
        <TreeNodeRow
          key={n.id}
          node={n}
          selected={selectedGroupId === n.id}
          selectedGroupId={selectedGroupId}
          onSelect={onSelect}
          depth={depth}
        />
      ))}
    </ul>
  );
}

function TreeNodeRow({
  node,
  selected,
  selectedGroupId,
  onSelect,
  depth,
}: {
  node: TreeNode;
  selected: boolean;
  selectedGroupId: number | null;
  onSelect: (id: number) => void;
  depth: number;
}) {
  const [open, setOpen] = useState(true);
  const hasChildren = node.children.length > 0;
  const hasAccounts = node.accounts.length > 0;
  // The row is one button, the chevron (when present) is a SIBLING
  // button. Nested <button> elements are invalid HTML and behave
  // unpredictably across browsers; rendering them adjacently keeps
  // both keyboard-reachable without nesting a button inside a button.
  return (
    <li className="flex items-stretch gap-1" style={{ paddingLeft: `${depth * 12}px` }}>
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => onSelect(node.id)}
        className={cn(
          "flex-1 flex items-center gap-1.5 px-2 py-1.5 rounded-lg text-[13px] text-left transition-colors",
          selected
            ? "bg-violet-500/15 text-white border border-violet-500/30"
            : "text-[#e8e8ef] hover:bg-white/[0.06] border border-transparent",
        )}
      >
        <span className="w-4 inline-flex items-center justify-center text-[#9aa0aa]">
          {(hasChildren || hasAccounts) ? (open ? <ChevronDown size={12} /> : <ChevronRight size={12} />) : null}
        </span>
        <Folder size={14} className="text-amber-300/80 shrink-0" />
        <span className="flex-1 truncate font-medium">{node.name}</span>
        {(hasAccounts || hasChildren) && (
          <span className="text-[10px] tabular-nums text-[#9aa0aa]">
            {hasAccounts ? node.accounts.length : ""}
            {hasAccounts && hasChildren ? " · " : ""}
            {hasChildren ? `${countDescendants(node)} sub` : ""}
          </span>
        )}
      </button>
      {(hasChildren || hasAccounts) && (
        <button
          type="button"
          aria-label={open ? "Collapse" : "Expand"}
          onClick={() => setOpen((v) => !v)}
          className="px-1.5 rounded-md hover:bg-white/[0.08] text-[#9aa0aa]"
        >
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </button>
      )}
      {open && hasChildren && (
        <div className="basis-full mt-1">
          <TreeView
            nodes={node.children}
            selectedGroupId={selectedGroupId}
            onSelect={onSelect}
            depth={depth + 1}
          />
        </div>
      )}
    </li>
  );
}

function countDescendants(n: TreeNode): number {
  return n.children.length + n.children.reduce((acc, c) => acc + countDescendants(c), 0);
}
