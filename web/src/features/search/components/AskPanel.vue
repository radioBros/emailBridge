<script setup lang="ts">
import { watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiClose, mdiForumOutline, mdiInformationOutline } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import { ASK_MAX_CHARS, useAskMailbox } from "@/features/search/composables/useAskMailbox";
import type { AskCitation } from "@/features/search/helpers/api";

const props = defineProps<{
  /** The account asked about (semantic search is available for it). */
  accountId: string;
  /** Its display name. */
  accountLabel: string;
  /** The Search page's message-date filter: excerpts only from messages in the range (also the way past the
   *  per-query chunk cap of a large mailbox). */
  from?: string;
  to?: string;
}>();

const emit = defineEmits<{
  close: [];
  open: [citation: AskCitation];
}>();

const { t } = useI18n();
const sendAction = actionOf("send");
const closeAction = actionOf("close");

const {
  question,
  result,
  asked,
  error,
  parts,
  canAsk,
  isFirstLoad,
  isRefreshing,
  isLoading,
  ask,
  reset,
} = useAskMailbox();

watch(
  () => props.accountId,
  () => reset(),
);

const submit = (): void => {
  void ask(props.accountId, { from: props.from, to: props.to });
};

const onKeydown = (e: KeyboardEvent): void => {
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
    e.preventDefault();
    submit();
  }
};

const citationAt = (messageId: string): AskCitation | undefined =>
  result.value?.citations.find((c) => c.messageId === messageId);

const openCitation = (messageId: string): void => {
  const c = citationAt(messageId);
  if (c) emit("open", c);
};
</script>

<template>
  <AppCard
    class="mb-4"
    data-testid="ask-panel"
  >
    <template #header>
      <div class="d-flex align-center ga-3 w-100 min-width-0">
        <v-avatar
          color="primary"
        >
          <v-icon
            :icon="mdiForumOutline"
          />
        </v-avatar>
        <div class="flex-grow-1 min-width-0">
          <div class="text-title-medium">
            {{ t("search.ask_title") }}
          </div>
          <p class="text-body-small text-medium-emphasis mb-0 text-truncate">
            {{ t("search.ask_account", { account: accountLabel }) }}
          </p>
        </div>
        <v-btn
          :icon="mdiClose"
          :color="closeAction.color"
          :aria-label="t('search.ask_close')"
          data-testid="ask-close"
          @click="emit('close')"
        />
      </div>
    </template>

    <div class="d-flex flex-column ga-3">
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("search.ask_hint") }}
      </p>
      <p
        v-if="from || to"
        class="text-body-small text-medium-emphasis mb-0"
        data-testid="ask-range"
      >
        {{ t("search.ask_range") }}
      </p>
      <v-textarea
        v-model="question"
        :label="t('search.ask_label')"
        :placeholder="t('search.ask_placeholder')"
        :counter="ASK_MAX_CHARS"
        :maxlength="ASK_MAX_CHARS"
        class="no-validation"
        rows="2"
        data-testid="ask-question"
        auto-grow
        @keydown="onKeydown"
      />
      <div class="d-flex flex-wrap align-center justify-end ga-3">
        <span class="text-body-small text-medium-emphasis">
          {{ t("search.ask_shortcut") }}
        </span>
        <v-btn
          :color="sendAction.color"
          :prepend-icon="sendAction.icon"
          :loading="isLoading"
          :disabled="!canAsk"
          data-testid="ask-submit"
          @click="submit"
        >
          {{ t("search.ask_submit") }}
        </v-btn>
      </div>

      <LoadingSkeleton
        v-if="isFirstLoad"
        :rows="3"
        shape="list"
      />

      <v-alert
        v-else-if="error && !result"
        type="error"
        data-testid="ask-error"
      >
        {{ error }}
      </v-alert>

      <div
        v-else-if="result"
        class="position-relative d-flex flex-column ga-3"
        data-testid="ask-answer"
      >
        <v-alert
          v-if="error"
          type="error"
        >
          {{ error }}
        </v-alert>
        <div class="text-body-small text-medium-emphasis">
          {{ asked }}
        </div>
        <p
          v-if="result.excerpts === 0"
          class="text-body-medium mb-0"
          data-testid="ask-nothing"
        >
          {{ t("search.ask_nothing_embedded") }}
        </p>
        <p
          v-else
          class="text-body-large mb-0 answer-text"
        >
          <template
            v-for="(part, i) in parts"
            :key="i"
          >
            <span v-if="part.kind === 'text'">{{ part.text }}</span>
            <sup v-else>
              <button
                :aria-label="t('search.ask_open_source', { n: part.n })"
                class="cite-ref text-primary font-weight-bold"
                type="button"
                @click="openCitation(part.messageId)"
              >
                [{{ part.n }}]
              </button>
            </sup>
          </template>
        </p>

        <div
          v-if="result.citations.length"
          class="d-flex flex-column ga-2"
        >
          <div class="text-label-large text-medium-emphasis">
            {{ t("search.ask_sources") }}
          </div>
          <div class="d-flex flex-wrap ga-2">
            <v-chip
              v-for="(c, i) in result.citations"
              :key="c.messageId"
              :prepend-icon="actionOf('open').icon"
              color="primary"
              data-testid="ask-citation"
              @click="emit('open', c)"
            >
              <span class="text-truncate citation-label">
                {{ i + 1 }}. {{ c.subject || t("search.no_subject") }} · {{ formatDateTime(c.date) }}
              </span>
            </v-chip>
          </div>
        </div>

        <div class="d-flex align-start ga-2 text-body-small text-medium-emphasis">
          <v-icon
            :icon="mdiInformationOutline"
          />
          <span>{{ t("search.ask_disclaimer") }}</span>
        </div>

        <AppSectionLoader :status="isRefreshing" />
      </div>
    </div>
  </AppCard>
</template>

<style scoped lang="scss">
.answer-text {
  max-width: 75ch;
  white-space: pre-line;
}

.cite-ref {
  background: none;
  border: 0;
  cursor: pointer;
  padding: 0 1px;
  border-radius: 4px;

  &:focus-visible {
    outline: 2px solid currentColor;
    outline-offset: 1px;
  }
}

.citation-label {
  max-width: 40ch;
}
</style>
