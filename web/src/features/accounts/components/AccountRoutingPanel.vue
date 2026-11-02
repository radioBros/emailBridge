<script setup lang="ts">
import { computed, onMounted, toRef, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiFlask, mdiWebhook } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { useAccountRouting } from "@/features/accounts/composables/useAccountRouting";

const props = defineProps<{ accountId: string }>();

const { t } = useI18n();
const retryAction = actionOf("retry");
const testAction = actionOf("test");

const accountId = toRef(props, "accountId");
const {
  routes,
  endpoints,
  rows,
  loadError,
  testing,
  testError,
  latestEvent,
  empty,
  isFirstLoad,
  isRefreshing,
  load,
  testWithLatest,
} = useAccountRouting(accountId);

onMounted(() => {
  void load();
});

watch(accountId, () => {
  void load();
});

const mappedText = (mapped: unknown): string => {
  if (mapped === undefined) return "";
  try {
    return JSON.stringify(mapped, null, 2);
  } catch {
    return String(mapped);
  }
};

const testErrorMessage = computed(() => {
  if (!testError.value) return "";
  if (testError.value === "no_event") return t("accounts.routing_no_event");
  return testError.value;
});
</script>

<template>
  <div class="position-relative">
    <p class="text-body-medium text-medium-emphasis mb-4">
      {{ t("accounts.routing_intro") }}
    </p>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="5"
      shape="list"
    />

    <v-alert
      v-else-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
      <template #append>
        <v-btn
          :color="retryAction.color"
          @click="load"
        >
          {{ t("accounts.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <div
      v-else-if="empty"
      class="text-body-medium text-medium-emphasis py-6 text-center"
    >
      {{ t("accounts.routing_empty") }}
    </div>

    <div
      v-else
      class="position-relative"
    >
      <div class="d-flex flex-wrap align-center justify-space-between ga-3 mb-4">
        <div class="text-body-small text-medium-emphasis">
          {{ t("accounts.routing_summary", {
            routes: routes.length,
            endpoints: endpoints.length,
          }) }}
        </div>
        <v-btn
          :color="testAction.color"
          :prepend-icon="mdiFlask"
          :loading="testing"
          :disabled="!routes.length"
          @click="testWithLatest"
        >
          {{ t("accounts.routing_test") }}
        </v-btn>
      </div>

      <v-alert
        v-if="testErrorMessage"
        type="warning"
        class="mb-4"
      >
        {{ testErrorMessage }}
      </v-alert>

      <v-alert
        v-else-if="latestEvent"
        type="info"
        class="mb-4"
      >
        {{ t("accounts.routing_tested_with", {
          type: latestEvent.type,
          id: latestEvent.id,
        }) }}
      </v-alert>

      <v-row>
        <v-col
          cols="12"
          md="4"
        >
          <AppCard class="mb-4 mb-md-0 h-100">
            <template #header>
              <div class="d-flex align-center ga-2">
                <v-icon-inline
                  :icon="mdiWebhook"
                  color="secondary"
                />
                <span class="text-title-medium font-weight-bold">
                  {{ t("accounts.routing_endpoints") }}
                </span>
              </div>
            </template>
            <div
              v-if="!endpoints.length"
              class="text-body-medium text-medium-emphasis"
            >
              {{ t("accounts.routing_endpoints_empty") }}
            </div>
            <v-list
              v-else
              class="bg-transparent py-0"
            >
              <v-list-item
                v-for="ep in endpoints"
                :key="ep.id"
                :title="ep.name"
                :subtitle="ep.url || ep.kind"
                rounded="lg"
              >
                <template #append>
                  <v-chip
                    :color="ep.enabled ? 'success' : 'default'"
                    label
                  >
                    {{ ep.enabled ? t("accounts.routing_enabled") : t("accounts.routing_disabled") }}
                  </v-chip>
                </template>
              </v-list-item>
            </v-list>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
          md="8"
        >
          <div class="d-flex flex-column ga-3">
            <AppCard
              v-for="row in rows"
              :key="row.route.id"
              class="h-auto"
            >
              <div class="d-flex flex-wrap align-start justify-space-between ga-2 mb-2">
                <div class="min-w-0">
                  <div class="text-title-small font-weight-bold">
                    {{ row.route.name }}
                  </div>
                  <div class="text-body-small text-medium-emphasis">
                    {{ t("accounts.routing_to") }}:
                    {{ row.endpointName }}
                    · {{ t("accounts.routing_order") }} {{ row.route.order }}
                  </div>
                </div>
                <div class="d-flex flex-wrap ga-2">
                  <v-chip
                    :color="row.route.enabled ? 'success' : 'default'"
                    label
                  >
                    {{ row.route.enabled ? t("accounts.routing_enabled") : t("accounts.routing_disabled") }}
                  </v-chip>
                  <v-chip
                    v-if="row.result"
                    :color="row.result.matched ? 'success' : 'warning'"
                    label
                  >
                    {{
                      row.result.matched
                        ? t("accounts.routing_matched")
                        : t("accounts.routing_not_matched")
                    }}
                  </v-chip>
                </div>
              </div>

              <div
                v-if="row.route.filter"
                class="text-caption font-mono text-medium-emphasis mb-2 text-truncate"
              >
                {{ row.route.filter }}
              </div>

              <v-alert
                v-if="row.error"
                type="error"
                class="mb-0"
              >
                {{ row.error }}
              </v-alert>

              <template v-else-if="row.result">
                <div
                  v-if="row.result.error"
                  class="text-body-small text-error mb-2"
                >
                  {{ row.result.error }}
                </div>
                <div
                  v-if="mappedText(row.result.mapped)"
                  class="mt-1"
                >
                  <div class="text-caption text-medium-emphasis mb-1">
                    {{ t("accounts.routing_mapped") }}
                  </div>
                  <pre class="text-caption font-mono mb-0 text-wrap">{{ mappedText(row.result.mapped) }}</pre>
                </div>
              </template>
            </AppCard>
          </div>
        </v-col>
      </v-row>

      <AppSectionLoader :status="isRefreshing || testing" />
    </div>
  </div>
</template>
