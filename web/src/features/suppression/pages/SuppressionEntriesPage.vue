<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { mdiArrowLeft, mdiShieldKey } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import { useNotifyStore } from "@/stores/notify";
import type { SuppressionEntryCreate, SuppressionEntryView } from "@/api/suppression";
import { useSuppressionEntries } from "@/features/suppression/composables/useSuppressionEntries";
import { sourceLabelKey } from "@/features/suppression/helpers/kinds";
import EntryFormDialog from "@/features/suppression/components/EntryFormDialog.vue";

const { t, te, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const confirm = useConfirm();
const notify = useNotifyStore();
const createAction = actionOf("create");
const deleteAction = actionOf("delete");
const importAction = actionOf("create");
const exportAction = actionOf("export");

const listId = computed(() => String(route.params.listId ?? ""));

const {
  list,
  rows,
  total,
  page,
  ipp,
  search,
  sortBy,
  loadError,
  mutating,
  formError,
  formFieldErrors,
  isFirstLoad,
  isRefreshing,
  loadList,
  load,
  setPage,
  setIpp,
  setSearch,
  add,
  remove,
  importCsv,
  exportCsv,
} = useSuppressionEntries();

const formOpen = ref(false);
const fileInput = ref<HTMLInputElement | null>(null);

const boot = async (): Promise<void> => {
  if (!listId.value) return;
  await loadList(listId.value);
  await load(listId.value);
};

onMounted(() => {
  void boot();
});

watch(listId, () => {
  void boot();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "address", title: t("suppression.col_address"), forced: true },
  { key: "reason", title: t("suppression.col_reason"), sortable: false },
  { key: "source", title: t("suppression.col_source"), sortable: false },
  { key: "at", title: t("suppression.col_at"), sortable: false },
  { key: "actions", title: t("suppression.col_actions"), sortable: false, align: "end" },
]);

const title = computed(() =>
  t("suppression.entries_title", { name: list.value?.name || listId.value }),
);

const sourceLabel = (s: string): string => {
  const key = sourceLabelKey(s);
  return te(key) ? t(key) : s;
};

const relativeAt = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const goBack = (): void => {
  void router.push({ name: "suppression" });
};

const onAdd = async (payload: SuppressionEntryCreate): Promise<void> => {
  const created = await add(listId.value, payload);
  if (created) formOpen.value = false;
};

const onDelete = async (row: SuppressionEntryView): Promise<void> => {
  const ok = await confirm({
    title: t("suppression.entry_delete_title"),
    body: t("suppression.entry_delete_body", { address: row.address }),
    variant: "danger",
    confirmText: t("suppression.delete"),
  });
  if (!ok) return;
  await remove(listId.value, row.id);
};

const onImportClick = (): void => {
  fileInput.value?.click();
};

const onFile = async (ev: Event): Promise<void> => {
  const input = ev.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  const text = await file.text();
  const res = await importCsv(listId.value, text);
  if (res) {
    notify.push({
      message: t("suppression.import_ok", {
        imported: res.imported,
        skipped: res.skipped,
        invalid: res.invalid,
      }),
      color: "success",
    });
  } else if (formError.value) {
    // The import error lands in `formError`, shown only by the (closed) entry dialog: notify it
    // and clear it so it does not reappear in the next "Add entry" dialog.
    notify.push({ message: formError.value, color: "error" });
    formError.value = null;
  }
};

const onExport = async (): Promise<void> => {
  const csv = await exportCsv(listId.value);
  if (csv == null) return;
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "suppression.csv";
  a.click();
  URL.revokeObjectURL(url);
};
</script>

<template>
  <div>
    <input
      ref="fileInput"
      type="file"
      accept=".csv,text/csv"
      class="d-none"
      @change="onFile"
    >

    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-6">
      <div class="min-w-0">
        <v-btn
          :prepend-icon="mdiArrowLeft"
          :color="actionOf('navigate').color"
          class="mb-2"
          @click="goBack"
        >
          {{ t("suppression.back") }}
        </v-btn>
        <h1 class="text-h5 font-weight-bold mb-1 d-flex align-center ga-2">
          <v-avatar
            color="warning"
          >
            <v-icon
              :icon="mdiShieldKey"
            />
          </v-avatar>
          {{ title }}
        </h1>
        <p class="text-body-small text-medium-emphasis mb-0">
          {{ t("suppression.import_pick") }}
        </p>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          :color="importAction.color"
          :prepend-icon="importAction.icon"
          :loading="mutating"
          data-testid="suppression-import"
          @click="onImportClick"
        >
          {{ t("suppression.entries_import") }}
        </v-btn>
        <v-btn
          :color="exportAction.color"
          :prepend-icon="exportAction.icon"
          :loading="mutating"
          data-testid="suppression-export"
          @click="onExport"
        >
          {{ t("suppression.entries_export") }}
        </v-btn>
        <v-btn
          :color="createAction.color"
          :prepend-icon="createAction.icon"
          data-testid="suppression-entry-add"
          @click="formOpen = true"
        >
          {{ t("suppression.entries_add") }}
        </v-btn>
      </div>
    </div>

    <v-alert
      v-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
    </v-alert>

    <TableCard
      :headers="headers"
      :items="rows"
      :total="total"
      :page="page"
      :ipp="ipp"
      :sort-by="sortBy"
      :search="search"
      :loading="isFirstLoad || isRefreshing"
      :grid="{ xs: 1, md: 2 }"
      table-name="suppression-entries"
      @update:page="(v) => setPage(listId, v)"
      @update:ipp="(v) => setIpp(listId, v)"
      @update:search="(v) => setSearch(listId, v)"
    >
      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("suppression.entries_empty") }}
        </div>
      </template>

      <template #card="{ item }">
        <v-card class="d-flex flex-column h-100">
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold text-wrap">
              {{ item.address }}
            </v-card-title>
            <v-card-subtitle>{{ relativeAt(item.at) }}</v-card-subtitle>
          </v-card-item>
          <v-card-text class="pt-0">
            <v-chip
              color="secondary"
            >
              {{ sourceLabel(item.source) }}
            </v-chip>
            <div
              v-if="item.reason"
              class="text-body-small mt-2"
            >
              {{ item.reason }}
            </div>
          </v-card-text>
          <v-card-actions class="px-4 pb-4 pt-0">
            <v-btn
              :color="deleteAction.color"
              :icon="deleteAction.icon"
              :aria-label="t('suppression.delete')"
              @click="onDelete(item)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.address`]="{ item }">
        <span class="font-weight-medium">{{ item.address }}</span>
      </template>

      <template #[`item.reason`]="{ item }">
        {{ item.reason || "—" }}
      </template>

      <template #[`item.source`]="{ item }">
        <v-chip
          color="secondary"
        >
          {{ sourceLabel(item.source) }}
        </v-chip>
      </template>

      <template #[`item.at`]="{ item }">
        <span class="text-body-small text-medium-emphasis">
          {{ relativeAt(item.at) }}
        </span>
      </template>

      <template #[`item.actions`]="{ item }">
        <v-btn
          :color="deleteAction.color"
          :icon="deleteAction.icon"
          :aria-label="t('suppression.delete')"
          @click="onDelete(item)"
        />
      </template>
    </TableCard>

    <EntryFormDialog
      v-model="formOpen"
      :submitting="mutating"
      :error-msg="formError"
      :field-errors="formFieldErrors"
      @submit="onAdd"
    />
  </div>
</template>
