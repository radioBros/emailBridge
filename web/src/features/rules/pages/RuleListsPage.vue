<script setup lang="ts">
/** Named lists for `inList()` (VIP domains, blocked senders, keywords): server-side search, sort, pages. */
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiFormatListBulletedSquare } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader, ServerTableSortItem } from "@/components/tables/types";
import {
  createRuleList,
  deleteRuleList,
  getRuleList,
  listRuleLists,
  updateRuleList,
  type RuleList,
  type RuleListKind,
} from "@/api/rules";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import DateUtil from "@/helpers/Date";
import { useNotifyStore } from "@/stores/notify";
import RuleListDialog from "@/features/rules/components/RuleListDialog.vue";

const { t, locale } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const confirm = useConfirm();
const load = useLoadState();
const createAction = actionOf("create");
const editAction = actionOf("edit");
const deleteAction = actionOf("delete");

const rows = ref<RuleList[]>([]);
const total = ref(0);
const page = ref(1);
const ipp = ref(25);
const sortBy = ref<ServerTableSortItem[]>([{ key: "name", order: "asc" }]);
const search = ref("");
const loadError = ref<string | null>(null);
const dialogOpen = ref(false);
const editing = ref<RuleList | null>(null);
const saving = ref(false);
const formError = ref<string | null>(null);

const fetch = async (): Promise<void> => {
  load.start();
  try {
    const s = sortBy.value[0];
    const res = await listRuleLists({
      page: page.value,
      limit: ipp.value,
      q: search.value || undefined,
      sort: s ? `${s.order === "desc" ? "-" : ""}${s.key}` : "name",
    });
    rows.value = res.data ?? [];
    total.value = res.total ?? rows.value.length;
    loadError.value = null;
    load.finish(true);
  } catch (e) {
    loadError.value = messageOf(e);
    load.finish(false);
  }
};

onMounted(fetch);

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("rules.lists.col_name"), forced: true },
  { key: "kind", title: t("rules.lists.col_kind"), sortable: false },
  { key: "entryCount", title: t("rules.lists.col_entries") },
  { key: "updatedAt", title: t("rules.lists.col_updated") },
  { key: "actions", title: t("rules.lists.col_actions"), sortable: false, align: "end" },
]);

const relative = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const KIND_COLOR: Record<RuleListKind, string> = { domains: "primary", addresses: "secondary", keywords: "accent" };

const openCreate = (): void => {
  editing.value = null;
  formError.value = null;
  dialogOpen.value = true;
};

const openEdit = async (row: RuleList): Promise<void> => {
  try {
    editing.value = await getRuleList(row.id);
    formError.value = null;
    dialogOpen.value = true;
  } catch (e) {
    notify.push({ message: messageOf(e), color: "error" });
  }
};

const onSubmit = async (body: { name: string; kind: RuleListKind; entries: string[] }): Promise<void> => {
  saving.value = true;
  try {
    if (editing.value) {
      await updateRuleList(editing.value.id, { name: body.name, entries: body.entries }, editing.value.version);
    } else {
      await createRuleList(body);
    }
    dialogOpen.value = false;
    notify.push({ message: t("rules.lists.saved", { name: body.name }), color: "success" });
    await fetch();
  } catch (e) {
    formError.value = messageOf(e);
  } finally {
    saving.value = false;
  }
};

const onDelete = async (row: RuleList): Promise<void> => {
  const ok = await confirm({
    title: t("rules.lists.delete_title", { name: row.name }),
    body: t("rules.lists.delete_body"),
    variant: "danger",
    confirmText: t("rules.lists.delete"),
  });
  if (!ok) return;
  try {
    await deleteRuleList(row.id, row.version);
    notify.push({ message: t("rules.lists.deleted", { name: row.name }), color: "success" });
    await fetch();
  } catch (e) {
    notify.push({ message: messageOf(e), color: "error" });
  }
};

const setPage = (p: number): void => {
  page.value = p;
  void fetch();
};
const setIpp = (n: number): void => {
  ipp.value = n;
  page.value = 1;
  void fetch();
};
const setSort = (s: ServerTableSortItem[]): void => {
  sortBy.value = s;
  page.value = 1;
  void fetch();
};
const setSearch = (q: string): void => {
  search.value = q;
  page.value = 1;
  void fetch();
};
</script>

<template>
  <div>
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-6">
      <div class="min-w-0">
        <h1 class="text-h5 font-weight-bold mb-1 d-flex align-center ga-2">
          <v-avatar color="accent">
            <v-icon :icon="mdiFormatListBulletedSquare" />
          </v-avatar>
          {{ t("rules.lists.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("rules.lists.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        data-testid="lists-create"
        @click="openCreate"
      >
        {{ t("rules.lists.create") }}
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
      :loading="load.isLoading.value"
      :grid="{ xs: 1, md: 2 }"
      table-name="rule-lists"
      @update:page="setPage"
      @update:ipp="setIpp"
      @update:sort-by="setSort"
      @update:search="setSearch"
    >
      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ search ? t("rules.lists.no_match") : t("rules.lists.empty") }}
        </div>
      </template>
      <template #[`item.name`]="{ item }">
        <code class="font-weight-medium list-name">{{ item.name }}</code>
      </template>
      <template #[`item.kind`]="{ item }">
        <v-chip :color="KIND_COLOR[item.kind]">
          {{ t(`rules.lists.kind_${item.kind}`) }}
        </v-chip>
      </template>
      <template #[`item.entryCount`]="{ item }">
        {{ item.entryCount.toLocaleString(locale) }}
      </template>
      <template #[`item.updatedAt`]="{ item }">
        <span class="text-no-wrap">{{ relative(item.updatedAt) }}</span>
      </template>
      <template #[`item.actions`]="{ item }">
        <div
          class="d-flex justify-end ga-1"
          @click.stop
        >
          <v-btn
            :color="editAction.color"
            :icon="editAction.icon"
            :aria-label="t('rules.lists.edit')"
            @click="openEdit(item)"
          />
          <v-btn
            :color="deleteAction.color"
            :icon="deleteAction.icon"
            :aria-label="t('rules.lists.delete')"
            @click="onDelete(item)"
          />
        </div>
      </template>
    </TableCard>

    <RuleListDialog
      v-model="dialogOpen"
      :list="editing"
      :saving="saving"
      :error-msg="formError"
      @submit="onSubmit"
    />
  </div>
</template>

<style scoped lang="scss">
.list-name {
  font-family: "JetBrains Mono", ui-monospace, monospace;
}
</style>
