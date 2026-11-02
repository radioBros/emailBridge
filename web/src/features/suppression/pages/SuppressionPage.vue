<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiShieldKey } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import type {
  SuppressionListCreate,
  SuppressionListUpdate,
  SuppressionListView,
} from "@/api/suppression";
import { useSuppressionLists } from "@/features/suppression/composables/useSuppressionLists";
import { kindColor, kindLabelKey, SUPPRESSION_KINDS } from "@/features/suppression/helpers/kinds";
import ListFormDialog from "@/features/suppression/components/ListFormDialog.vue";

const { t, te, locale } = useI18n();
const router = useRouter();
const confirm = useConfirm();
const createAction = actionOf("create");
const editAction = actionOf("edit");
const deleteAction = actionOf("delete");
const openAction = actionOf("open");

const {
  rows,
  total,
  page,
  ipp,
  search,
  kind,
  sortBy,
  loadError,
  mutating,
  formError,
  formFieldErrors,
  isFirstLoad,
  isRefreshing,
  load,
  setPage,
  setIpp,
  setSortBy,
  setSearch,
  setKind,
  create,
  update,
  remove,
} = useSuppressionLists();

const formOpen = ref(false);
const editing = ref<SuppressionListView | null>(null);

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("suppression.col_name"), forced: true },
  { key: "kind", title: t("suppression.col_kind"), sortable: false },
  { key: "updatedAt", title: t("suppression.col_updated") },
  { key: "actions", title: t("suppression.col_actions"), sortable: false, align: "end" },
]);

const kindFilterItems = computed(() => [
  { title: t("suppression.kind_all"), value: "" },
  ...SUPPRESSION_KINDS.map((k) => ({ title: t(`suppression.kind_${k}`), value: k })),
]);

const kindLabel = (k: string): string => {
  const key = kindLabelKey(k);
  return te(key) ? t(key) : k;
};

const relativeAt = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const openCreate = (): void => {
  editing.value = null;
  formError.value = null;
  formOpen.value = true;
};

const openEdit = (row: SuppressionListView): void => {
  editing.value = row;
  formError.value = null;
  formOpen.value = true;
};

const openEntries = (row: SuppressionListView): void => {
  void router.push({ name: "suppression-entries", params: { listId: row.id } });
};

const onSubmit = async (
  payload: SuppressionListCreate | (SuppressionListUpdate & { id: string }),
): Promise<void> => {
  if ("id" in payload) {
    const { id, ...body } = payload;
    const ok = await update(id, body);
    if (ok) formOpen.value = false;
    return;
  }
  const created = await create(payload);
  if (created) formOpen.value = false;
};

const onDelete = async (row: SuppressionListView): Promise<void> => {
  const ok = await confirm({
    title: t("suppression.delete_title"),
    body: t("suppression.delete_body", { name: row.name }),
    variant: "danger",
    confirmText: t("suppression.delete"),
  });
  if (!ok) return;
  await remove(row.id);
};
</script>

<template>
  <div>
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-6">
      <div class="min-w-0">
        <h1 class="text-h5 font-weight-bold mb-1 d-flex align-center ga-2">
          <v-avatar
            color="warning"
          >
            <v-icon
              :icon="mdiShieldKey"
            />
          </v-avatar>
          {{ t("suppression.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("suppression.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        data-testid="suppression-create"
        @click="openCreate"
      >
        {{ t("suppression.create") }}
      </v-btn>
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
      table-name="suppression-lists"
      @update:page="setPage"
      @update:ipp="setIpp"
      @update:sort-by="setSortBy"
      @update:search="setSearch"
    >
      <template #toolbar>
        <v-select
          :model-value="kind"
          :items="kindFilterItems"
          :label="t('suppression.filter_kind')"
          class="no-validation"
          style="max-width: 220px"
          hide-details
          @update:model-value="setKind"
        />
        <v-btn
          :color="createAction.color"
          :prepend-icon="createAction.icon"
          class="d-md-none"
          @click="openCreate"
        >
          {{ t("suppression.create") }}
        </v-btn>
      </template>

      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("suppression.empty") }}
        </div>
      </template>

      <template #card="{ item }">
        <v-card class="d-flex flex-column h-100">
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold text-wrap">
              {{ item.name }}
            </v-card-title>
            <v-card-subtitle>{{ relativeAt(item.updatedAt) }}</v-card-subtitle>
          </v-card-item>
          <v-card-text class="pt-0">
            <v-chip
              :color="kindColor(item.kind)"
            >
              {{ kindLabel(item.kind) }}
            </v-chip>
          </v-card-text>
          <v-card-actions class="px-4 pb-4 pt-0">
            <v-btn
              :color="openAction.color"
              :icon="openAction.icon"
              :aria-label="t('suppression.open')"
              @click="openEntries(item)"
            />
            <v-btn
              :color="editAction.color"
              :icon="editAction.icon"
              :aria-label="t('suppression.edit')"
              @click="openEdit(item)"
            />
            <v-btn
              :color="deleteAction.color"
              :icon="deleteAction.icon"
              :aria-label="t('suppression.delete')"
              @click="onDelete(item)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.name`]="{ item }">
        <a
          class="font-weight-medium text-decoration-none"
          href="#"
          @click.prevent="openEntries(item)"
        >
          {{ item.name }}
        </a>
      </template>

      <template #[`item.kind`]="{ item }">
        <v-chip
          :color="kindColor(item.kind)"
        >
          {{ kindLabel(item.kind) }}
        </v-chip>
      </template>

      <template #[`item.updatedAt`]="{ item }">
        <span class="text-body-small text-medium-emphasis">
          {{ relativeAt(item.updatedAt) }}
        </span>
      </template>

      <template #[`item.actions`]="{ item }">
        <div class="d-flex justify-end ga-1">
          <v-btn
            :color="openAction.color"
            :icon="openAction.icon"
            :aria-label="t('suppression.open')"
            @click="openEntries(item)"
          />
          <v-btn
            :color="editAction.color"
            :icon="editAction.icon"
            :aria-label="t('suppression.edit')"
            @click="openEdit(item)"
          />
          <v-btn
            :color="deleteAction.color"
            :icon="deleteAction.icon"
            :aria-label="t('suppression.delete')"
            @click="onDelete(item)"
          />
        </div>
      </template>
    </TableCard>

    <ListFormDialog
      v-model="formOpen"
      :list="editing"
      :submitting="mutating"
      :error-msg="formError"
      :field-errors="formFieldErrors"
      @submit="onSubmit"
    />
  </div>
</template>
