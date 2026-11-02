<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import { actionOf } from "@/constants/Actions";
import { combine, email as emailRules, required } from "@/constants/Validations";
import { useNotifyStore } from "@/stores/notify";
import { useProblem } from "@/composables/useProblem";
import { ApiProblemError } from "@/api/client";
import { searchAccounts, type AccountView } from "@/api/outbox";
import { submitOutbound } from "@/api/outboxSubmit";
import {
  htmlToText,
  isEmptyHtml,
  sanitizeHtml,
} from "@/features/outbox/helpers/richText";

const RichTextEditor = defineAsyncComponent(
  () => import("@/features/outbox/components/RichTextEditor.vue"),
);

const open = defineModel<boolean>("open", { default: false });
const emit = defineEmits<{ sent: [] }>();

const { t } = useI18n();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const sendAction = actionOf("send");
const cancelAction = actionOf("cancel");
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);

const accountId = ref("");
const accountOptions = ref<AccountView[]>([]);
const accountsLoading = ref(false);
const to = ref("");
const subject = ref("");
const html = ref("<p></p>");
const trackOpens = ref(true);
const trackClicks = ref(true);
const listId = ref("");
const sending = ref(false);

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
);

const loadAccounts = async (q = ""): Promise<void> => {
  accountsLoading.value = true;
  try {
    accountOptions.value = await searchAccounts(q);
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    accountsLoading.value = false;
  }
};

watch(open, (v) => {
  if (v) {
    void loadAccounts();
    to.value = "";
    subject.value = "";
    html.value = "<p></p>";
    trackOpens.value = true;
    trackClicks.value = true;
    listId.value = "";
    accountId.value = "";
  }
});

const send = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  if (!accountId.value) return;
  if (isEmptyHtml(html.value)) {
    notify.push({ message: t("outbox.compose.body_required"), color: "error" });
    return;
  }
  sending.value = true;
  try {
    const clean = sanitizeHtml(html.value);
    await submitOutbound(accountId.value, {
      to: [{ address: to.value.trim() }],
      subject: subject.value.trim(),
      html: clean,
      text: htmlToText(clean),
      trackOpens: trackOpens.value,
      trackClicks: trackClicks.value,
      listId: listId.value.trim() || undefined,
    });
    notify.push({ message: t("outbox.compose.sent"), color: "success" });
    open.value = false;
    emit("sent");
  } catch (err) {
    const msg =
      err instanceof ApiProblemError ? err.problem.title : t("outbox.compose.error");
    notify.push({ message: msg || t("outbox.compose.error"), color: "error" });
  } finally {
    sending.value = false;
  }
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('outbox.compose.title')"
    :max-width="720"
    :loading="sending"
    fullscreen-on-mobile
  >
    <p class="text-body-medium text-medium-emphasis mb-4">
      {{ t("outbox.compose.subtitle") }}
    </p>
    <v-form
      ref="formRef"
      @submit.prevent
    >
      <AutocompleteSearch
        v-model="accountId"
        :items="accountItems"
        :loading="accountsLoading"
        :label="t('outbox.compose.account')"
        :rules="required"
        class="mb-2"
        server-search
        @update:search="loadAccounts"
      />
      <v-text-field
        v-model="to"
        :label="t('outbox.compose.to')"
        :rules="combine(required, emailRules)"
        type="email"
        class="mb-2"
      />
      <v-text-field
        v-model="subject"
        :label="t('outbox.compose.subject')"
        :rules="required"
        class="mb-2"
      />
      <div class="mb-3">
        <Suspense>
          <RichTextEditor
            v-model="html"
            :label="t('outbox.compose.html')"
            :disabled="sending"
          />
          <template #fallback>
            <v-skeleton-loader type="article" />
          </template>
        </Suspense>
      </div>
      <div class="text-label-large mb-2">
        {{ t("outbox.compose.tracking") }}
      </div>
      <v-switch
        v-model="trackOpens"
        :label="t('outbox.compose.track_opens')"
        class="no-validation"
        color="primary"
        hide-details
      />
      <v-switch
        v-model="trackClicks"
        :label="t('outbox.compose.track_clicks')"
        class="no-validation mb-2"
        color="primary"
        hide-details
      />
      <v-text-field
        v-model="listId"
        :label="t('outbox.compose.list_id')"
        :hint="t('outbox.compose.list_id_hint')"
        class="no-validation"
        persistent-hint
      />
    </v-form>

    <template #footer>
      <v-btn
        :color="cancelAction.color"
        @click="open = false"
      >
        {{ t("outbox.compose.cancel") }}
      </v-btn>
      <v-btn
        :color="sendAction.color"
        :prepend-icon="sendAction.icon"
        :loading="sending"
        @click="send"
      >
        {{ t("outbox.compose.send") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
