<script setup lang="ts">
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiFileDocumentOutline } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import type { TemplateView } from "@/api/templates";
import { useTemplates } from "@/features/templates/composables/useTemplates";
import TemplateRowActions from "@/features/templates/components/TemplateRowActions.vue";
import { formatOf, type TemplateFormatFields } from "@/features/templates/helpers/mjml";

const { t, locale } = useI18n();
const router = useRouter();
const confirm = useConfirm();
const createAction = actionOf("create");

const {
  rows,
  total,
  page,
  ipp,
  search,
  sortBy,
  loadError,
  isFirstLoad,
  isRefreshing,
  load,
  setPage,
  setIpp,
  setSortBy,
  setSearch,
  remove,
} = useTemplates();

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("templates.col_name"), forced: true },
  { key: "currentVersion", title: t("templates.col_version"), sortable: false },
  { key: "updatedAt", title: t("templates.col_updated") },
  { key: "actions", title: t("templates.col_actions"), sortable: false, align: "end" },
]);

const isMjml = (row: TemplateView & TemplateFormatFields): boolean => formatOf(row) === "mjml";

const relativeAt = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const openCreate = (): void => {
  void router.push({ name: "templates-new" });
};

const openEdit = (row: TemplateView): void => {
  void router.push({ name: "templates-edit", params: { templateId: row.id } });
};

const onDelete = async (row: TemplateView): Promise<void> => {
  const ok = await confirm({
    title: t("templates.delete_title"),
    body: t("templates.delete_body", { name: row.name }),
    variant: "danger",
    confirmText: t("templates.delete"),
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
            color="success"
          >
            <v-icon
              :icon="mdiFileDocumentOutline"
            />
          </v-avatar>
          {{ t("templates.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("templates.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        data-testid="templates-create"
        @click="openCreate"
      >
        {{ t("templates.create") }}
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
      table-name="templates"
      @update:page="setPage"
      @update:ipp="setIpp"
      @update:sort-by="setSortBy"
      @update:search="setSearch"
    >
      <template #toolbar>
        <v-btn
          :color="createAction.color"
          :prepend-icon="createAction.icon"
          class="d-md-none"
          @click="openCreate"
        >
          {{ t("templates.create") }}
        </v-btn>
      </template>

      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("templates.empty") }}
        </div>
      </template>

      <template #card="{ item }">
        <v-card class="d-flex flex-column h-100">
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold text-wrap d-flex align-center flex-wrap ga-2">
              {{ item.name }}
              <v-chip
                v-if="isMjml(item)"
                color="accent"
                data-testid="templates-mjml-chip"
              >
                {{ t("templates.format_mjml") }}
              </v-chip>
            </v-card-title>
            <v-card-subtitle class="text-wrap">
              {{ t("templates.version_n", { n: item.currentVersion }) }}
              ·
              {{ relativeAt(item.updatedAt) }}
            </v-card-subtitle>
          </v-card-item>
          <v-card-actions class="px-4 pb-4 pt-0">
            <TemplateRowActions
              :item="item"
              @edit="openEdit(item)"
              @delete="onDelete(item)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.name`]="{ item }">
        <span class="d-inline-flex align-center flex-wrap ga-2">
          <span class="font-weight-medium text-wrap">{{ item.name }}</span>
          <v-chip
            v-if="isMjml(item)"
            color="accent"
            data-testid="templates-mjml-chip"
          >
            {{ t("templates.format_mjml") }}
          </v-chip>
        </span>
      </template>

      <template #[`item.currentVersion`]="{ item }">
        <v-chip
          color="secondary"
        >
          {{ t("templates.version_n", { n: item.currentVersion }) }}
        </v-chip>
      </template>

      <template #[`item.updatedAt`]="{ item }">
        <span class="text-body-small text-medium-emphasis">
          {{ relativeAt(item.updatedAt) }}
        </span>
      </template>

      <template #[`item.actions`]="{ item }">
        <TemplateRowActions
          :item="item"
          @edit="openEdit(item)"
          @delete="onDelete(item)"
        />
      </template>
    </TableCard>
  </div>
</template>
