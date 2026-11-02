<script setup lang="ts">
/**
 * Detail of one notification: what, how often, since when, structured data, and the actions the
 * caller may take (open the affected screen, mark read, resolve). The row already carries every
 * field (there is no GET by id), so there is nothing to load; actions show their own progress.
 */
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { useRouter } from "vue-router";
import { mdiCheck, mdiEmailOpenOutline } from "@mdi/js";
import type { Notification } from "@/api/notifications";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import { actionOf } from "@/constants/Actions";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import DateUtil from "@/helpers/Date";
import {
  canResolve,
  categorySpec,
  detailOf,
  routeForLink,
  severitySpec,
  titleOf,
} from "@/features/notifications/helpers/catalog";
import { useNotificationFeed } from "@/features/notifications/stores/feed";

const props = defineProps<{
  modelValue: boolean;
  item: Notification | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  changed: [item: Notification];
}>();

const { t, te } = useI18n();
const { width } = useDisplay();
const router = useRouter();
const feed = useNotificationFeed();
const session = useSessionStore();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const openAction = actionOf("open");

const busy = ref<"read" | "resolve" | null>(null);

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const drawerWidth = computed(() => Math.min(480, width.value));

const spec = computed(() => (props.item ? categorySpec(props.item.category) : null));
const sev = computed(() => (props.item ? severitySpec(props.item.severity) : null));
const title = computed(() => (props.item ? titleOf(props.item, t, te) : ""));
const detail = computed(() => (props.item ? detailOf(props.item, t, te) : ""));
const target = computed(() => (props.item ? routeForLink(router, props.item.link) : null));
const resolvable = computed(() => (props.item ? canResolve(props.item, session.me) : false));
const unread = computed(() => Boolean(props.item && props.item.unread !== false && props.item.state === "open"));

const enumLabel = (group: string, value: string): string => {
  const key = `notifications.${group}.${value}`;
  return te(key) ? t(key) : value;
};

const facts = computed(() => {
  const n = props.item;
  if (!n) return [];
  const rows: Array<{ label: string; value: string }> = [
    { label: t("notifications.col.category"), value: enumLabel("category", n.category) },
    { label: t("notifications.col.occurrences"), value: String(n.occurrences) },
    { label: t("notifications.field.first_seen"), value: DateUtil.formatDateTime(n.firstSeen) },
    { label: t("notifications.field.last_seen"), value: DateUtil.formatDateTime(n.lastSeen) },
  ];
  if (n.openedAt && n.openedAt !== n.firstSeen) {
    rows.push({ label: t("notifications.field.opened_at"), value: DateUtil.formatDateTime(n.openedAt) });
  }
  if (n.resolvedAt) {
    rows.push({ label: t("notifications.field.resolved_at"), value: DateUtil.formatDateTime(n.resolvedAt) });
  }
  rows.push({
    label: t("notifications.field.scope"),
    value: n.scope === "instance" ? t("notifications.scope.instance") : t("notifications.scope.organization"),
  });
  return rows;
});

const dataRows = computed(() =>
  Object.entries(props.item?.data ?? {}).map(([k, v]) => ({
    key: k,
    value: v !== null && typeof v === "object" ? JSON.stringify(v) : String(v ?? ""),
  })),
);

const goTo = (): void => {
  if (!target.value) return;
  open.value = false;
  void router.push(target.value);
};

const markRead = async (): Promise<void> => {
  const n = props.item;
  if (!n || busy.value) return;
  busy.value = "read";
  try {
    await feed.markRead([n.id]);
    emit("changed", { ...n, unread: false });
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    busy.value = null;
  }
};

const resolve = async (): Promise<void> => {
  const n = props.item;
  if (!n || busy.value) return;
  busy.value = "resolve";
  try {
    const out = await feed.resolve(n.id);
    emit("changed", { ...n, ...out });
    notify.push({ message: t("notifications.resolve_done"), color: "success" });
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    busy.value = null;
  }
};
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="title"
    :width="drawerWidth"
    :loading="busy !== null"
  >
    <template
      v-if="spec"
      #prepend
    >
      <v-avatar :color="item?.state === 'open' ? spec.color : undefined">
        <v-icon :icon="spec.icon" />
      </v-avatar>
    </template>

    <div
      v-if="item && sev"
      class="d-flex flex-column ga-5"
      data-testid="notification-detail"
    >
      <div class="d-flex flex-wrap align-center ga-2">
        <v-chip
          :color="sev.color"
          :prepend-icon="sev.icon"
          label
        >
          {{ enumLabel("severity", item.severity) }}
        </v-chip>
        <v-chip
          :color="item.state === 'open' ? 'warning' : 'success'"
          label
        >
          {{ enumLabel("state", item.state) }}
        </v-chip>
        <v-chip
          v-if="unread"
          color="primary"
          label
        >
          {{ t("notifications.unread") }}
        </v-chip>
      </div>

      <p
        v-if="detail"
        class="text-body-large mb-0"
      >
        {{ detail }}
      </p>

      <dl class="notification-facts">
        <template
          v-for="row in facts"
          :key="row.label"
        >
          <dt class="text-body-small text-medium-emphasis">
            {{ row.label }}
          </dt>
          <dd class="text-body-medium ma-0">
            {{ row.value }}
          </dd>
        </template>
      </dl>

      <div v-if="dataRows.length">
        <div class="text-label-large font-weight-bold mb-2">
          {{ t("notifications.field.data") }}
        </div>
        <dl class="notification-facts">
          <template
            v-for="row in dataRows"
            :key="row.key"
          >
            <dt class="text-body-small text-medium-emphasis font-family-monospace">
              {{ row.key }}
            </dt>
            <dd class="text-body-medium font-family-monospace ma-0 notification-facts__value">
              {{ row.value }}
            </dd>
          </template>
        </dl>
      </div>
    </div>

    <template
      v-if="item"
      #footer
    >
      <v-btn
        v-if="unread"
        :loading="busy === 'read'"
        :disabled="busy !== null"
        :prepend-icon="mdiEmailOpenOutline"
        color="secondary"
        data-testid="notification-detail-read"
        @click="markRead"
      >
        {{ t("notifications.mark_read") }}
      </v-btn>
      <v-btn
        v-if="resolvable"
        :loading="busy === 'resolve'"
        :disabled="busy !== null"
        :prepend-icon="mdiCheck"
        color="success"
        data-testid="notification-detail-resolve"
        @click="resolve"
      >
        {{ t("notifications.resolve") }}
      </v-btn>
      <v-btn
        v-if="target"
        :prepend-icon="openAction.icon"
        :color="openAction.color"
        data-testid="notification-detail-open"
        @click="goTo"
      >
        {{ t("notifications.open_link") }}
      </v-btn>
    </template>
  </AppDrawerPanel>
</template>

<style scoped lang="scss">
.notification-facts {
  display: grid;
  grid-template-columns: minmax(7rem, max-content) minmax(0, 1fr);
  gap: 8px 16px;
  align-items: baseline;
}

.notification-facts__value {
  overflow-wrap: anywhere;
}
</style>
