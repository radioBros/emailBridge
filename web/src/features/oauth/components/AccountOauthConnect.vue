<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiGoogle, mdiMicrosoft } from "@mdi/js";
import { listOauthApps, startAccountOauth, type OauthApp } from "@/api/oauth";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { actionOf } from "@/constants/Actions";

const props = defineProps<{
  accountId: string;
}>();

const { t } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const apps = ref<OauthApp[]>([]);
const loading = ref(false);
const starting = ref(false);

onMounted(async () => {
  loading.value = true;
  try {
    const res = await listOauthApps({ page: 1, limit: 50, includeTotal: false });
    apps.value = (res.data ?? []).filter((a) => a.enabled);
  } catch (e) {
    apps.value = [];
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    loading.value = false;
  }
});

const appOf = (provider: string): OauthApp | undefined =>
  apps.value.find((a) => a.provider === provider);

const start = async (provider: "gmail" | "outlook"): Promise<void> => {
  const app = appOf(provider);
  if (!app || !props.accountId) return;
  starting.value = true;
  try {
    const res = await startAccountOauth(props.accountId, app.id);
    window.location.assign(res.authorizeUrl);
  } catch (e) {
    notify.push({ message: messageOf(e) || t("oauth.connect_error"), color: "error" });
  } finally {
    starting.value = false;
  }
};
</script>

<template>
  <div
    v-if="apps.length"
    class="d-flex flex-wrap ga-2"
  >
    <v-btn
      v-if="appOf('gmail')"
      :prepend-icon="mdiGoogle"
      :color="actionOf('verify').color"
      :loading="starting"
      :disabled="loading || !accountId"
      @click="start('gmail')"
    >
      {{ t("oauth.connect_google") }}
    </v-btn>
    <v-btn
      v-if="appOf('outlook')"
      :prepend-icon="mdiMicrosoft"
      :color="actionOf('verify').color"
      :loading="starting"
      :disabled="loading || !accountId"
      @click="start('outlook')"
    >
      {{ t("oauth.connect_microsoft") }}
    </v-btn>
  </div>
</template>
