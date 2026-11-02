<script setup lang="ts">
import { CHROME_ICON_COLOR } from "@/core/config/IconColors";
import { useI18n } from "vue-i18n";
import { mdiCheck, mdiTranslate } from "@mdi/js";
import { LOCALES } from "@/app/locales";
import { ensureLocale } from "@/app/i18n";

const { t, locale } = useI18n();

const onSelect = async (code: string): Promise<void> => {
  if (code === locale.value) return;
  await ensureLocale(code);
};
</script>

<template>
  <v-menu location="bottom end">
    <template #activator="{ props: menu }">
      <v-btn
        v-tippy="t('shell.locale')"
        :icon="mdiTranslate"
        :color="CHROME_ICON_COLOR.language"
        :aria-label="t('shell.locale')"
        v-bind="menu"
      />
    </template>
    <v-list
      :aria-label="t('shell.locale')"
      min-width="200"
      role="listbox"
    >
      <v-list-item
        v-for="item in LOCALES"
        :key="item.code"
        :active="locale === item.code"
        :title="item.nativeName"
        :aria-selected="locale === item.code"
        color="primary"
        role="option"
        @click="onSelect(item.code)"
      >
        <template
          v-if="locale === item.code"
          #append
        >
          <v-icon-inline
            :icon="mdiCheck"
            color="primary"
          />
        </template>
      </v-list-item>
    </v-list>
  </v-menu>
</template>
