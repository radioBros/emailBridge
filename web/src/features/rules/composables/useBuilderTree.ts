import { inject, provide, type InjectionKey } from "vue";
import type { RuleGroup, RuleLeaf, RuleMatch, RuleNode } from "@/api/rules";
import { MAX_DEPTH, newGroup, newLeaf } from "@/features/rules/model/builder";

/**
 * Edits the builder tree IN PLACE (so row identity, focus and keys survive typing). Rows never mutate
 * their props; they call these operations, which the root `FilterBuilder` provides and which report
 * every change through `onChange`.
 */
export interface BuilderTreeOps {
  patch: (node: RuleNode, patch: Partial<RuleLeaf> | Partial<RuleGroup>) => void;
  replace: (group: RuleGroup, index: number, node: RuleNode) => void;
  add: (group: RuleGroup, node?: RuleNode) => void;
  addGroup: (group: RuleGroup, match?: RuleMatch) => void;
  remove: (group: RuleGroup, index: number) => void;
  move: (group: RuleGroup, index: number, delta: number) => void;
  /** Moves a row into a new nested group of its own (the "group" action). */
  wrap: (group: RuleGroup, index: number) => void;
  /** Replaces a nested group by its children. */
  unwrap: (parent: RuleGroup, index: number) => void;
  canNest: (depth: number) => boolean;
}

const KEY: InjectionKey<BuilderTreeOps> = Symbol("builderTree");

export const createBuilderTree = (onChange: () => void): BuilderTreeOps => {
  const changed = <T>(fn: () => T): T => {
    const out = fn();
    onChange();
    return out;
  };
  return {
    patch: (node, patch) =>
      changed(() => {
        for (const [k, v] of Object.entries(patch)) {
          if (v === undefined) delete (node as unknown as Record<string, unknown>)[k];
          else (node as unknown as Record<string, unknown>)[k] = v;
        }
      }),
    replace: (group, index, node) => changed(() => group.conditions.splice(index, 1, node)),
    add: (group, node) => changed(() => group.conditions.push(node ?? newLeaf("subject"))),
    addGroup: (group, match = "any") =>
      changed(() => group.conditions.push(newGroup(match, [newLeaf("subject")]))),
    remove: (group, index) => changed(() => group.conditions.splice(index, 1)),
    move: (group, index, delta) =>
      changed(() => {
        const to = index + delta;
        if (to < 0 || to >= group.conditions.length) return;
        const [n] = group.conditions.splice(index, 1);
        group.conditions.splice(to, 0, n!);
      }),
    wrap: (group, index) =>
      changed(() => {
        const n = group.conditions[index];
        if (!n) return;
        group.conditions.splice(index, 1, newGroup("any", [n]));
      }),
    unwrap: (parent, index) =>
      changed(() => {
        const g = parent.conditions[index] as RuleGroup | undefined;
        if (!g?.conditions) return;
        parent.conditions.splice(index, 1, ...g.conditions);
      }),
    canNest: (depth) => depth < MAX_DEPTH,
  };
};

export const provideBuilderTree = (ops: BuilderTreeOps): void => provide(KEY, ops);

export const useBuilderTree = (): BuilderTreeOps => {
  const ops = inject(KEY, null);
  if (!ops) throw new Error("useBuilderTree outside FilterBuilder");
  return ops;
};
