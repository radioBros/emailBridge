// =========================================================================
// useSettingsStore — server-synced UI preferences (column visibility/order/widths).
//
// Source of truth: GET/PATCH /api/v1/me/settings (your API, see README §5). A
// local copy is kept in Pinia (persisted, cleared on logout) so components read
// it without a round trip and a settings outage never stops a table rendering.
//
// Shape: { tables: { [tableName]: { visibility: string[]; order: string[];
//                                   widths?: Record<string, number> } } }
// `widths` are fractions (0–1) of the table's resizable width, by column key
// (see composables/useColumnResize); older px values are read as weights.
//
// The backend must DEEP-MERGE a PATCH (or saving one table wipes every other
// table's) and treat `null` as delete (JSON Merge Patch, RFC 7396).
//
// Needs `pinia-plugin-persistedstate` v4 (`pick`; v3 called it `paths`).
// =========================================================================

import { defineStore } from 'pinia';
import { ref } from 'vue';
import { api } from '@/api/client';

export interface TableConfig {
  /** Visible column keys. */
  visibility: string[];
  /** FULL column order, hidden columns included. */
  order: string[];
  /**
   * Column widths as fractions (0–1) of the resizable width, by header key — set once the user
   * resizes a column. A layout saved on one screen therefore fills any other.
   */
  widths?: Record<string, number>;
}

export interface UiSettings {
  tables: Record<string, TableConfig>;
}

const SETTINGS_PATH = '/me/settings';

export const useSettingsStore = defineStore(
  'settings',
  () => {
    const tables = ref<Record<string, TableConfig>>({});
    const loaded = ref(false);

    // In-flight de-duplication: every ColManager on a page awaits load() in its
    // own onMounted, and `loaded` is only set AFTER the await, so N tables
    // would fire N identical GETs. Sharing the promise makes the first caller
    // do the work and the rest wait for it.
    let inFlight: Promise<void> | null = null;

    /** Fetches the saved settings once (or again with `force`). Never rejects. */
    const load = (force = false): Promise<void> => {
      if (loaded.value && !force) return Promise.resolve();
      if (inFlight && !force) return inFlight;
      inFlight = (async () => {
        try {
          // The body is the settings object, or wraps it (`{ settings, updatedAt, version }`).
          const raw = await api.get<Partial<UiSettings> & { settings?: Partial<UiSettings> }>(SETTINGS_PATH);
          tables.value = (raw?.settings ?? raw)?.tables ?? {};
        } catch {
          // Non-fatal: keep the locally persisted layout and fall back to column defaults.
        } finally {
          loaded.value = true;
          inFlight = null;
        }
      })();
      return inFlight;
    };

    /** Persists one table's slice (fire-and-forget: local state is already correct). */
    const save = (
      tableName: string,
      slice: Partial<Omit<TableConfig, 'widths'>> & { widths?: Record<string, number | null> | null },
    ): void => {
      api.patch(SETTINGS_PATH, { tables: { [tableName]: slice } }).catch(() => {
        /* ignore — local state is still correct */
      });
    };

    /**
     * `currentOrder` is the table's FULL order, hidden columns included. Seeding
     * the order from the VISIBLE keys drops the column just hidden, so it comes
     * back at the far right when shown again. It is written every time:
     * ColManager's order is the saved one already healed (mergeColumnOrder), so
     * rewriting it also repairs layouts saved by that bug.
     */
    const setVisibility = (tableName: string, keys: string[], currentOrder: string[]): void => {
      tables.value[tableName] = { ...tables.value[tableName], order: currentOrder, visibility: keys };
      save(tableName, tables.value[tableName]);
    };

    const setOrder = (tableName: string, keys: string[]): void => {
      tables.value[tableName] = {
        ...tables.value[tableName],
        visibility: tables.value[tableName]?.visibility ?? keys,
        order: keys,
      };
      save(tableName, tables.value[tableName]);
    };

    /**
     * Column widths (fractions of the resizable width) by key, written once a resize ends (see
     * useColumnResize). The map replaces the saved one: a merge-patching backend keeps keys that are
     * merely absent, so keys that were dropped are sent explicitly as `null`.
     */
    const setWidths = (tableName: string, widths: Record<string, number>): void => {
      const current = tables.value[tableName];
      const keys = Object.keys(widths);
      const dropped = Object.keys(current?.widths ?? {}).filter((k) => !(k in widths));
      tables.value[tableName] = {
        ...current,
        visibility: current?.visibility ?? keys,
        order: current?.order ?? keys,
        widths,
      };
      save(tableName, {
        ...tables.value[tableName],
        widths: { ...widths, ...Object.fromEntries(dropped.map((k) => [k, null])) },
      });
    };

    /**
     * Back to the automatic layout. A merge-patching backend keeps keys that are
     * merely absent, so the deletion is sent explicitly as `null`.
     */
    const resetWidths = (tableName: string): void => {
      const current = tables.value[tableName];
      if (!current?.widths) return;
      const { widths: _dropped, ...rest } = current;
      tables.value[tableName] = rest;
      save(tableName, { widths: null });
    };

    const getTable = (tableName: string): TableConfig | undefined => tables.value[tableName];

    /** On logout: the next user must not inherit this one's layouts. */
    const reset = (): void => {
      tables.value = {};
      loaded.value = false;
    };

    return { tables, loaded, load, setVisibility, setOrder, setWidths, resetWidths, getTable, reset };
  },
  {
    persist: {
      // Prefix with your app name if several apps share an origin.
      key: 'ui_settings',
      pick: ['tables'],
    },
  },
);
