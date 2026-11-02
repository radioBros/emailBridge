<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiCertificate } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import PecReceiptDrawer from "@/features/pec/components/PecReceiptDrawer.vue";
import PecSignatureBadge from "@/features/pec/components/PecSignatureBadge.vue";
import PecTypeChip from "@/features/pec/components/PecTypeChip.vue";
import { usePecReceipts } from "@/features/pec/composables/usePecReceipts";
import { PEC_RECEIPT_TYPES } from "@/features/pec/helpers/receiptTypes";

const props = defineProps<{
  accountId: string;
}>();

const { t, te } = useI18n();
const router = useRouter();

const {
  rows,
  total,
  page,
  ipp,
  sortBy,
  typeFilter,
  loadError,
  isFirstLoad,
  isRefreshing,
  load,
  setPage,
  setIpp,
  setSortBy,
  setTypeFilter,
  setSearch,
} = usePecReceipts(() => props.accountId);

const drawerOpen = ref(false);
const selectedId = ref<string | null>(null);
const searchDraft = ref("");
let searchTimer: ReturnType<typeof setTimeout> | null = null;

onMounted(() => {
  void load();
});

watch(
  () => props.accountId,
  () => {
    void load();
  },
);

const headers = computed<ServerTableHeader[]>(() => [
  { key: "at", title: t("pec.col_at"), sortable: true },
  { key: "type", title: t("pec.col_type"), sortable: true },
  { key: "deliveredTo", title: t("pec.col_recipient"), sortable: false },
  { key: "transportId", title: t("pec.col_transport"), sortable: false },
  { key: "signature", title: t("pec.col_signature"), sortable: false },
  { key: "outboundId", title: t("pec.col_outbound"), sortable: false },
]);

const typeItems = computed(() =>
  PEC_RECEIPT_TYPES.map((type) => ({
    title: te(`pec.type_${type}`) ? t(`pec.type_${type}`) : type,
    value: type,
  })),
);

const onSearchInput = (v: string): void => {
  searchDraft.value = v;
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    void setSearch(searchDraft.value);
  }, 300);
};

const onType = (v: unknown): void => {
  void setTypeFilter(typeof v === "string" ? v : null);
};

const onRow = (_e: Event, row: { item: { id?: string } }): void => {
  const id = row.item?.id;
  if (!id) return;
  selectedId.value = id;
  drawerOpen.value = true;
};

const onOutboundLink = (outboundId: string, ev?: Event): void => {
  ev?.stopPropagation();
  void router.push({ path: "/outbox", query: { id: outboundId } });
};

const onOutboundClick = (outboundId: string): void => {
  onOutboundLink(outboundId);
};

const onOpenOutbound = (outboundId: string): void => {
  drawerOpen.value = false;
  void router.push({ path: "/outbox", query: { id: outboundId } });
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-2 mb-4">
      <div class="d-flex align-center ga-2 min-w-0">
        <v-avatar
          color="info"
        >
          <v-icon
            :icon="mdiCertificate"
          />
        </v-avatar>
        <div class="min-w-0">
          <div class="text-title-medium font-weight-bold">
            {{ t("pec.tab_title") }}
          </div>
          <div class="text-body-small text-medium-emphasis">
            {{ t("pec.tab_subtitle") }}
          </div>
        </div>
      </div>
    </div>

    <v-row
      class="mb-2"
    >
      <v-col
        cols="12"
        sm="5"
        md="4"
      >
        <AutocompleteSearch
          :model-value="typeFilter"
          :items="typeItems"
          :label="t('pec.filter_type')"
          class="no-validation"
          clearable
          hide-details
          @update:model-value="onType"
        />
      </v-col>
      <v-col
        cols="12"
        sm="7"
        md="5"
      >
        <v-text-field
          :model-value="searchDraft"
          :label="t('pec.filter_q')"
          :placeholder="t('pec.filter_q_hint')"
          class="no-validation"
          clearable
          hide-details
          @update:model-value="onSearchInput"
        />
      </v-col>
    </v-row>

    <v-alert
      v-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
    </v-alert>

    <TableCard
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      :headers="headers"
      :items="rows"
      :total="total"
      :loading="isFirstLoad || isRefreshing"
      :searchable="false"
      table-name="pec-receipts"
      item-value="id"
      @update:page="(v) => setPage(v)"
      @update:ipp="(v) => setIpp(v)"
      @update:sort-by="(v) => setSortBy(v)"
      @click:row="onRow"
    >
      <template #[`item.at`]="{ item }">
        {{ DateUtil.formatDateTime(item.at) }}
      </template>
      <template #[`item.type`]="{ item }">
        <PecTypeChip :type="item.type" />
      </template>
      <template #[`item.deliveredTo`]="{ item }">
        {{ item.deliveredTo || EMPTY_VALUE }}
      </template>
      <template #[`item.transportId`]="{ item }">
        <span class="font-family-monospace text-body-small text-break">
          {{ item.transportId || EMPTY_VALUE }}
        </span>
      </template>
      <template #[`item.signature`]="{ item }">
        <PecSignatureBadge :signature="item.signature" />
      </template>
      <template #[`item.outboundId`]="{ item }">
        <v-btn
          v-if="item.outboundId"
          color="primary"
          class="px-1"
          @click.stop="onOutboundClick(item.outboundId!)"
        >
          {{ item.outboundId }}
        </v-btn>
        <span
          v-else
          class="text-medium-emphasis"
        >{{ EMPTY_VALUE }}</span>
      </template>
      <template #no-data>
        <div class="text-center py-8 px-4 text-medium-emphasis">
          <div class="text-body-large mb-1">
            {{ t("pec.empty_title") }}
          </div>
          <div class="text-body-medium">
            {{ t("pec.empty_body") }}
          </div>
        </div>
      </template>
    </TableCard>

    <PecReceiptDrawer
      v-model="drawerOpen"
      :receipt-id="selectedId"
      @open-outbound="onOpenOutbound"
    />
  </div>
</template>
