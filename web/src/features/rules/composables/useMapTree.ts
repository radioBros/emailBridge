import { inject, provide, type InjectionKey } from "vue";
import { freshKey, type MapEntry, type MapNode } from "@/features/rules/model/mapTree";

/** In-place edits of the payload tree (same reasoning as the builder tree: rows keep identity). */
export interface MapTreeOps {
  setEntryNode: (entry: MapEntry, node: MapNode) => void;
  renameKey: (entry: MapEntry, key: string) => void;
  patchNode: (node: MapNode, patch: Record<string, unknown>) => void;
  addEntry: (obj: Extract<MapNode, { kind: "object" }>) => void;
  removeEntry: (obj: Extract<MapNode, { kind: "object" }>, index: number) => void;
  moveEntry: (obj: Extract<MapNode, { kind: "object" }>, index: number, delta: number) => void;
  addItem: (list: Extract<MapNode, { kind: "list" }>) => void;
  setItem: (list: Extract<MapNode, { kind: "list" }>, index: number, node: MapNode) => void;
  removeItem: (list: Extract<MapNode, { kind: "list" }>, index: number) => void;
  moveItem: (list: Extract<MapNode, { kind: "list" }>, index: number, delta: number) => void;
  setRoot: (node: MapNode | null) => void;
}

const KEY: InjectionKey<MapTreeOps> = Symbol("mapTree");

const move = <T>(arr: T[], index: number, delta: number): void => {
  const to = index + delta;
  if (to < 0 || to >= arr.length) return;
  const [x] = arr.splice(index, 1);
  arr.splice(to, 0, x!);
};

export const createMapTree = (onChange: () => void, setRoot: (node: MapNode | null) => void): MapTreeOps => {
  const changed = (fn: () => void): void => {
    fn();
    onChange();
  };
  return {
    setEntryNode: (entry, node) => changed(() => (entry.node = node)),
    renameKey: (entry, key) => changed(() => (entry.key = key)),
    patchNode: (node, patch) => changed(() => Object.assign(node, patch)),
    addEntry: (obj) =>
      changed(() => obj.entries.push({ key: freshKey(obj.entries), node: { kind: "field", path: "data.subject" } })),
    removeEntry: (obj, index) => changed(() => obj.entries.splice(index, 1)),
    moveEntry: (obj, index, delta) => changed(() => move(obj.entries, index, delta)),
    addItem: (list) => changed(() => list.items.push({ kind: "constant", value: "" })),
    setItem: (list, index, node) => changed(() => list.items.splice(index, 1, node)),
    removeItem: (list, index) => changed(() => list.items.splice(index, 1)),
    moveItem: (list, index, delta) => changed(() => move(list.items, index, delta)),
    setRoot: (node) => changed(() => setRoot(node)),
  };
};

export const provideMapTree = (ops: MapTreeOps): void => provide(KEY, ops);

export const useMapTree = (): MapTreeOps => {
  const ops = inject(KEY, null);
  if (!ops) throw new Error("useMapTree outside MapEditor");
  return ops;
};
