<script setup lang="ts">
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { mdiAccountKey } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import { useAuditLog } from "@/features/organization/composables/useAuditLog";
import { useOrgRole } from "@/features/organization/composables/useOrgRole";
import AuditDetailDrawer from "@/features/organization/components/AuditDetailDrawer.vue";
import PermissionState from "@/features/organization/components/PermissionState.vue";

const { t, locale } = useI18n();
const org = useOrgRole();
const {
  items,
  total,
  page,
  ipp,
  search,
  actorId,
  action,
  targetId,
  from,
  to,
  sortBy,
  loadError,
  forbidden,
  selected,
  tableLoading,
  load,
  applyFilters,
} = useAuditLog();

const drawerOpen = computed({
  get: () => Boolean(selected.value),
  set: (v: boolean) => {
    if (!v) selected.value = null;
  },
});

onMounted(() => {
  if (!org.canReadAudit.value) {
    forbidden.value = true;
    return;
  }
  void load();
});

const headers = computed(() => [
  { key: "at", title: t("organization.audit.col_time"), sortable: true },
  { key: "actor", title: t("organization.audit.col_actor"), sortable: false },
  { key: "action", title: t("organization.audit.col_action"), sortable: false },
  { key: "target", title: t("organization.audit.col_target"), sortable: false },
  { key: "ip", title: t("organization.audit.col_ip"), sortable: false },
]);

const formatWhen = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toFormat("dd LLL yyyy HH:mm:ss") ?? iso;
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t("organization.audit.title") }}
      </h1>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("organization.audit.subtitle") }}
      </p>
    </div>

    <PermissionState
      v-if="forbidden || !org.canReadAudit.value"
      :title="t('organization.no_permission_title')"
      :body="t('organization.audit.no_permission')"
    />

    <template v-else>
      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load"
          >
            {{ t("organization.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <v-alert
        type="info"
        class="mb-4"
      >
        {{ t("organization.audit.export_note") }}
      </v-alert>

      <v-row class="mb-2">
        <v-col
          cols="12"
          sm="6"
          md="2"
        >
          <v-text-field
            v-model="actorId"
            :label="t('organization.audit.filter_actor')"
            class="no-validation"
            clearable
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
          md="2"
        >
          <v-text-field
            v-model="action"
            :label="t('organization.audit.filter_action')"
            class="no-validation"
            clearable
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
          md="2"
        >
          <v-text-field
            v-model="targetId"
            :label="t('organization.audit.filter_resource')"
            class="no-validation"
            clearable
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
          md="3"
        >
          <v-text-field
            v-model="from"
            :label="t('organization.audit.filter_from')"
            type="datetime-local"
            class="no-validation"
            clearable
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
          md="3"
        >
          <div class="d-flex ga-2">
            <v-text-field
              v-model="to"
              :label="t('organization.audit.filter_to')"
              type="datetime-local"
              class="no-validation flex-grow-1"
              clearable
              hide-details
            />
            <v-btn
              :color="actionOf('apply').color"
              @click="applyFilters"
            >
              {{ t("organization.apply") }}
            </v-btn>
          </div>
        </v-col>
      </v-row>

      <TableCard
        v-model:page="page"
        v-model:ipp="ipp"
        v-model:sort-by="sortBy"
        v-model:search="search"
        :headers="headers"
        :items="items"
        :total="total"
        :loading="tableLoading"
        table-name="audit-log"
        item-value="id"
        @update:search="() => { page = 1; void load(); }"
        @click:row="(_e, row) => { selected = row.item; }"
      >
        <template #toolbar>
          <div class="d-flex align-center ga-2">
            <v-avatar-small
              color="info"
            >
              <v-icon
                :icon="mdiAccountKey"
              />
            </v-avatar-small>
            <span class="text-title-small font-weight-bold">
              {{ t("organization.audit.title") }}
            </span>
          </div>
        </template>

        <template #no-data>
          <div class="d-flex flex-column align-center text-center py-6 ga-3">
            <v-avatar-hero
              color="info"
            >
              <v-icon
                :icon="mdiAccountKey"
              />
            </v-avatar-hero>
            <p class="text-body-medium text-medium-emphasis mb-0">
              {{ t("organization.audit.empty") }}
            </p>
          </div>
        </template>

        <template #[`item.at`]="{ item }">
          {{ formatWhen(item.at) }}
        </template>
      </TableCard>
    </template>

    <AuditDetailDrawer
      v-model:open="drawerOpen"
      :record="selected"
    />
  </div>
</template>
