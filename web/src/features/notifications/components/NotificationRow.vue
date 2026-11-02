<script setup lang="ts">
/**
 * One notification as a list row: category disc, title, short detail, then time · occurrences ·
 * resolved. Used by the bell panel; the centre's phone cards reuse it inside their card.
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { Notification } from "@/api/notifications";
import DateUtil from "@/helpers/Date";
import { categorySpec, detailOf, titleOf } from "@/features/notifications/helpers/catalog";

const props = defineProps<{
  item: Notification;
}>();

const emit = defineEmits<{
  select: [item: Notification];
}>();

const { t, te, locale } = useI18n();

const spec = computed(() => categorySpec(props.item.category));
const title = computed(() => titleOf(props.item, t, te));
const detail = computed(() => detailOf(props.item, t, te));
const resolved = computed(() => props.item.state !== "open");
const unread = computed(() => props.item.unread !== false && !resolved.value);

const when = computed(() => {
  const iso = resolved.value ? props.item.resolvedAt || props.item.lastSeen : props.item.lastSeen;
  return DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);
});

const occurrences = computed(() =>
  props.item.occurrences > 1 ? t("notifications.occurrences", { count: props.item.occurrences }) : "",
);
</script>

<template>
  <v-list-item
    :class="{ 'notification-row--resolved': resolved }"
    :aria-label="unread ? t('notifications.row_unread', { title }) : title"
    class="notification-row py-3"
    data-testid="notification-row"
    link
    @click="emit('select', item)"
  >
    <template #prepend>
      <v-avatar-small
        :color="resolved ? undefined : spec.color"
        class="me-3"
      >
        <v-icon :icon="spec.icon" />
      </v-avatar-small>
    </template>

    <div class="d-flex align-start ga-2 min-w-0">
      <div class="flex-grow-1 min-w-0">
        <div class="text-body-medium notification-row__clamp">
          <span :class="unread ? 'font-weight-bold' : 'font-weight-medium'">
            {{ title }}
          </span>
        </div>
        <div
          v-if="detail"
          class="text-body-small text-medium-emphasis notification-row__clamp mt-1"
        >
          {{ detail }}
        </div>
        <div class="d-flex flex-wrap align-center ga-2 mt-2 text-body-small text-medium-emphasis">
          <span>{{ when }}</span>
          <span
            v-if="occurrences"
            class="font-weight-medium"
          >
            {{ occurrences }}
          </span>
          <v-chip
            v-if="resolved"
            color="success"
            label
          >
            {{ t("notifications.state.resolved") }}
          </v-chip>
        </div>
      </div>
      <span
        v-if="unread"
        :aria-label="t('notifications.unread')"
        class="notification-row__dot bg-primary rounded-circle flex-shrink-0 mt-2"
        role="img"
      />
    </div>
  </v-list-item>
</template>

<style scoped lang="scss">
.notification-row__clamp {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.notification-row__dot {
  width: 8px;
  height: 8px;
}

.notification-row--resolved {
  opacity: 0.62;
}
</style>
