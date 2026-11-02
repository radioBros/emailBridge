<script setup lang="ts">
/**
 * Folder / label value: the context account's REAL mailboxes (special-use first), the virtual
 * "Inbox (any provider)" choice for folders, Gmail's system labels for labels, and "Other…" for a path
 * typed by hand (accounts not synced yet).
 */
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiArrowLeft, mdiInboxMultipleOutline, mdiPencilOutline, mdiTagOutline } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { MailboxView } from "@/api/messages";
import AccountSelect from "@/features/rules/components/AccountSelect.vue";
import { folderIcon } from "@/features/rules/components/labels";
import { SPECIAL_USES, VIRTUAL_INBOX } from "@/features/rules/model/builder";
import { useRuleContext } from "@/features/rules/composables/useRuleContext";

const props = withDefaults(
  defineProps<{
    modelValue: unknown;
    kind?: "folder" | "label";
    multiple?: boolean;
    /** Offer the virtual Inbox choice (folder rows with is / is not). */
    virtualInbox?: boolean;
  }>(),
  { kind: "folder", multiple: false, virtualInbox: false },
);

const emit = defineEmits<{
  "update:modelValue": [value: unknown];
}>();

const OTHER = "__other__";
const GMAIL_LABELS = ["INBOX", "SENT", "IMPORTANT", "STARRED", "UNREAD", "SPAM", "TRASH", "CATEGORY_PERSONAL", "CATEGORY_SOCIAL", "CATEGORY_PROMOTIONS", "CATEGORY_UPDATES", "CATEGORY_FORUMS"];

const { t } = useI18n();
const ctx = useRuleContext();
const mailboxes = ref<MailboxView[]>([]);
const loading = ref(false);
const typing = ref(false);

const accountId = computed({
  get: (): string => ctx.contextAccountId.value,
  set: (v: string): void => {
    ctx.contextAccountId.value = v || "";
  },
});

watch(
  accountId,
  async (id) => {
    if (!id) {
      mailboxes.value = [];
      return;
    }
    loading.value = true;
    await ctx.ensureAccount(id);
    mailboxes.value = await ctx.mailboxes(id);
    loading.value = false;
  },
  { immediate: true },
);

const rank = (m: MailboxView): number => {
  const i = SPECIAL_USES.indexOf(m.specialUse ?? "");
  return i === -1 ? SPECIAL_USES.length : i;
};

interface Item {
  title: string;
  value: string;
  subtitle?: string;
  icon: string;
  [key: string]: unknown;
}

const items = computed((): Item[] => {
  const out: Item[] = [];
  if (props.virtualInbox && !props.multiple) {
    out.push({ title: t("rules.builder.inbox_any"), value: VIRTUAL_INBOX, subtitle: t("rules.builder.inbox_any_hint"), icon: mdiInboxMultipleOutline });
  }
  const gmail = props.kind === "label" && (ctx.isGmail(accountId.value) || !accountId.value);
  if (gmail) {
    for (const l of GMAIL_LABELS) out.push({ title: l, value: l, subtitle: t("rules.builder.gmail_label"), icon: mdiTagOutline });
  }
  const seen = new Set(out.map((i) => i.value));
  for (const m of [...mailboxes.value].sort((a, b) => rank(a) - rank(b) || a.path.localeCompare(b.path))) {
    if (seen.has(m.path)) continue;
    seen.add(m.path);
    out.push({
      title: m.path,
      value: m.path,
      subtitle: m.specialUse ? t("rules.builder.special_use", { use: m.specialUse.replace("\\", "") }) : undefined,
      icon: folderIcon(m.specialUse),
    });
  }
  // A saved value the list does not have (another account, not synced yet) stays selectable.
  for (const v of Array.isArray(props.modelValue) ? props.modelValue : [props.modelValue]) {
    if (typeof v === "string" && v && !seen.has(v)) {
      seen.add(v);
      out.push({ title: v, value: v, icon: folderIcon() });
    }
  }
  if (!props.multiple) out.push({ title: t("rules.builder.other_path"), value: OTHER, icon: mdiPencilOutline });
  return out;
});

const value = computed({
  get: () => props.modelValue as string | string[] | null,
  set: (v) => {
    if (v === OTHER) {
      typing.value = true;
      emit("update:modelValue", "");
      return;
    }
    emit("update:modelValue", v);
  },
});

const textValue = computed({
  get: (): string => (typeof props.modelValue === "string" ? props.modelValue : ""),
  set: (v: string): void => emit("update:modelValue", v),
});
</script>

<template>
  <div class="d-flex flex-column ga-2">
    <AccountSelect
      v-if="!accountId"
      v-model="accountId"
      :label="t('rules.builder.list_folders_of')"
    />
    <v-text-field
      v-if="typing"
      v-model="textValue"
      :label="kind === 'label' ? t('rules.builder.label_name') : t('rules.builder.folder_path')"
      :placeholder="kind === 'label' ? 'Label_123' : 'INBOX/Clients'"
      class="no-validation"
      autocomplete="off"
      hide-details
      autofocus
    >
      <template #append-inner>
        <v-btn-affix
          :icon="mdiArrowLeft"
          :aria-label="t('rules.builder.back_to_list')"
          @click="typing = false"
        />
      </template>
    </v-text-field>
    <AutocompleteSearch
      v-else
      v-model="value"
      :items="items"
      :label="kind === 'label' ? t('rules.builder.label') : t('rules.builder.folder')"
      :loading="loading"
      :multiple="multiple"
      :chips="multiple"
      :closable-chips="multiple"
      :search-fields="['subtitle']"
      :data-testid="`rule-${kind}-select`"
      class="no-validation"
      hide-details
    >
      <template #item="{ props: itemProps, item }">
        <v-list-item
          :title="(item as Item).title"
          :subtitle="(item as Item).subtitle"
          :prepend-icon="(item as Item).icon"
          :class="{ 'text-primary font-weight-medium': (item as Item).value === VIRTUAL_INBOX }"
          v-bind="itemProps"
        />
      </template>
    </AutocompleteSearch>
  </div>
</template>
