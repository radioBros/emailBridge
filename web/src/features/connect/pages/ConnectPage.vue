<script setup lang="ts">
/* eslint-disable vue/no-v-html -- branding.headerHtml is server-sanitised */
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute } from "vue-router";
import { useTheme } from "vuetify";
import {
  mdiCheckCircle,
  mdiEmailOutline,
  mdiLockOutline,
  mdiMailboxOutline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import ThemeToggle from "@/components/common/ThemeToggle.vue";
import LanguageMenu from "@/app/components/LanguageMenu.vue";
import { actionOf } from "@/constants/Actions";
import { combine, email as emailRules, required } from "@/constants/Validations";
import { useLoadState } from "@/composables/useLoadState";
import { applyConnectPalette } from "@/composables/useBrandTheme";
import {
  bootstrapConnect,
  connectErrorCode,
  discoverConnect,
  oauthStartConnect,
  passwordConnect,
  redeemConnectLinkURL,
  type AutoconfigResult,
  type ConnectBootstrap,
  type ConnectOauthApp,
} from "@/api/connect";

const { t } = useI18n();
const route = useRoute();
const theme = useTheme();
const { isFirstLoad, start, finish } = useLoadState();
const createAction = actionOf("create");

const token = computed(() => String(route.params.token ?? ""));
const boot = ref<ConnectBootstrap | null>(null);
const step = ref<"email" | "auth" | "done">("email");
const email = ref("");
const password = ref("");
const displayName = ref("");
const discovery = ref<AutoconfigResult | null>(null);
const loading = ref(false);
const errorKey = ref<string | null>(null);
const doneRedirect = ref<string | null>(null);
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const faviconEl = ref<HTMLLinkElement | null>(null);

const brandName = computed(
  () => boot.value?.branding.brandName?.trim() || t("connect.title"),
);
const primary = computed(() => boot.value?.branding.primaryColor || undefined);
const logoSrc = computed(() => {
  const b = boot.value?.branding;
  if (!b) return undefined;
  const dark = theme.global.current.value.dark;
  if (dark && b.logoDarkUrl) return b.logoDarkUrl;
  return b.logoUrl || undefined;
});
const headerHtml = computed(() => boot.value?.branding.headerHtml || "");
const oauthApps = computed(() => boot.value?.oauthApps ?? []);
const injectedHead: HTMLElement[] = [];

const setFavicon = (href: string | undefined): void => {
  if (typeof document === "undefined") return;
  if (!href) {
    faviconEl.value?.remove();
    faviconEl.value = null;
    return;
  }
  let el = faviconEl.value;
  if (!el) {
    el = document.createElement("link");
    el.setAttribute("rel", "icon");
    el.setAttribute("data-eb-connect-favicon", "1");
    document.head.appendChild(el);
    faviconEl.value = el;
  }
  el.href = href;
};

const injectHeadHtml = (html: string): void => {
  clearInjectedHead();
  if (!html || typeof document === "undefined") return;
  const tpl = document.createElement("template");
  tpl.innerHTML = html;
  tpl.content.childNodes.forEach((node) => {
    if (!(node instanceof HTMLElement)) return;
    const tag = node.tagName.toLowerCase();
    if (tag !== "meta" && tag !== "link" && tag !== "style") return;
    const el = node.cloneNode(true) as HTMLElement;
    el.setAttribute("data-eb-connect-head", "1");
    document.head.appendChild(el);
    injectedHead.push(el);
  });
};

const clearInjectedHead = (): void => {
  injectedHead.splice(0).forEach((el) => el.remove());
};

const errorFromCode = (code: string): string => {
  if (code === "connect_link_invalid") return "connect.invalid";
  if (code === "feature_disabled") return "connect.disabled";
  if (code === "connect_csrf_invalid") return "connect.error_csrf";
  if (code === "validation_failed" || code === "invalid_credentials") {
    return "connect.error_validation";
  }
  return "connect.error_generic";
};

const load = async (): Promise<void> => {
  start();
  errorKey.value = null;
  if (!token.value) {
    errorKey.value = "connect.invalid";
    finish(false);
    return;
  }
  try {
    boot.value = await bootstrapConnect(token.value);
    injectHeadHtml(boot.value.branding.headHtml || "");
    applyConnectPalette(
      theme,
      boot.value.branding.palette,
      boot.value.branding.primaryColor,
    );
    setFavicon(boot.value.branding.faviconUrl);
    finish(true);
    // P6.1 oauth-bound links: redeem via API (SPA owns /connect/:token).
    if (boot.value.mode === "oauth") {
      window.location.assign(redeemConnectLinkURL(token.value));
    }
  } catch (err) {
    errorKey.value = errorFromCode(connectErrorCode(err));
    finish(false);
  }
};

onMounted(() => {
  void load();
});

onBeforeUnmount(() => {
  clearInjectedHead();
  setFavicon(undefined);
});

watch(primary, (color) => {
  if (!color || typeof document === "undefined") return;
  document.documentElement.style.setProperty("--eb-connect-accent", color);
});

watch(
  () => boot.value?.branding.faviconUrl,
  (href) => setFavicon(href),
);

const onDiscover = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  if (!boot.value) return;
  loading.value = true;
  errorKey.value = null;
  try {
    discovery.value = await discoverConnect(token.value, {
      email: email.value.trim(),
      csrf: boot.value.csrf,
    });
    step.value = "auth";
  } catch (err) {
    errorKey.value = errorFromCode(connectErrorCode(err));
  } finally {
    loading.value = false;
  }
};

const onPassword = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  if (!boot.value) return;
  loading.value = true;
  errorKey.value = null;
  try {
    const done = await passwordConnect(token.value, {
      email: email.value.trim(),
      password: password.value,
      name: displayName.value.trim() || undefined,
      csrf: boot.value.csrf,
      imap: discovery.value?.imap,
      smtp: discovery.value?.smtp,
    });
    step.value = "done";
    doneRedirect.value = done.redirectUrl ?? null;
    if (done.redirectUrl) {
      window.setTimeout(() => {
        window.location.assign(done.redirectUrl!);
      }, 1200);
    }
  } catch (err) {
    errorKey.value = errorFromCode(connectErrorCode(err));
  } finally {
    loading.value = false;
  }
};

const onOauth = async (app: ConnectOauthApp): Promise<void> => {
  if (!boot.value) return;
  loading.value = true;
  errorKey.value = null;
  try {
    const res = await oauthStartConnect(token.value, {
      email: email.value.trim(),
      appId: app.id,
      csrf: boot.value.csrf,
    });
    window.location.assign(res.authorizeUrl);
  } catch (err) {
    errorKey.value = errorFromCode(connectErrorCode(err));
    loading.value = false;
  }
};

const oauthLabel = (app: ConnectOauthApp): string => {
  if (app.provider === "gmail") return t("connect.oauth_google");
  if (app.provider === "outlook") return t("connect.oauth_microsoft");
  return t("connect.oauth_other", { name: app.name });
};

const serverLine = (kind: "imap" | "smtp"): string => {
  const s = discovery.value?.[kind];
  if (!s) return "—";
  return `${s.host}:${s.port} (${s.tls})`;
};
</script>

<template>
  <div class="connect-page d-flex align-center justify-center pa-4 pa-md-8">
    <AppCard
      class="connect-card"
      content-class="pa-6 pa-md-8"
      elevated
    >
      <template #header>
        <div class="d-flex align-center ga-3">
          <v-avatar-lead
            :color="primary || 'primary'"
          >
            <v-img
              v-if="logoSrc"
              :src="logoSrc"
              alt=""
            />
            <v-icon
              v-else
              :icon="mdiMailboxOutline"
            />
          </v-avatar-lead>
          <div class="min-w-0 flex-grow-1">
            <div class="text-title-large font-weight-bold truncate-1">
              {{ brandName }}
            </div>
            <div class="text-body-small text-medium-emphasis">
              {{ t("connect.subtitle") }}
            </div>
          </div>
          <ThemeToggle />
          <LanguageMenu />
        </div>
      </template>

      <LoadingSkeleton
        v-if="isFirstLoad"
        shape="form"
      />

      <template v-else>
        <v-alert
          v-if="errorKey && !boot"
          type="error"
          class="mb-0"
          role="alert"
        >
          <div class="d-flex flex-column flex-sm-row align-sm-center ga-3">
            <div class="flex-grow-1 min-w-0">
              {{ t(errorKey) }}
            </div>
            <v-btn
              :color="actionOf('retry').color"
              class="align-self-start align-self-sm-center"
              @click="load"
            >
              {{ t("connect.retry") }}
            </v-btn>
          </div>
        </v-alert>

        <template v-else-if="boot">
          <v-alert
            v-if="errorKey"
            type="error"
            class="mb-4"
            role="alert"
          >
            {{ t(errorKey) }}
          </v-alert>

          <!-- Server-sanitised branding HTML (bluemonday allow-list). -->
          <div
            v-if="headerHtml"
            class="connect-header-html mb-4"
            v-html="headerHtml" 
          />

          <v-form
            v-if="step !== 'done'"
            ref="formRef"
            @submit.prevent
          >
            <template v-if="step === 'email'">
              <v-text-field
                v-model="email"
                :label="t('connect.email')"
                :rules="combine(required, emailRules)"
                :prepend-inner-icon="mdiEmailOutline"
                type="email"
                autocomplete="username"
                class="mb-2"
              />
              <v-btn
                :color="createAction.color"
                :loading="loading"
                block
                @click="onDiscover"
              >
                {{ t("connect.continue") }}
              </v-btn>
            </template>

            <template v-else-if="step === 'auth'">
              <div class="text-body-medium mb-2">
                {{ email }}
              </div>
              <div
                v-if="discovery?.imap || discovery?.smtp"
                class="mb-4"
              >
                <div class="text-label-large mb-1">
                  {{ t("connect.servers") }}
                </div>
                <div class="text-body-small text-medium-emphasis">
                  {{ t("connect.imap") }}: {{ serverLine("imap") }}
                </div>
                <div class="text-body-small text-medium-emphasis mb-1">
                  {{ t("connect.smtp") }}: {{ serverLine("smtp") }}
                </div>
                <div
                  v-if="discovery?.source && discovery.source !== 'none'"
                  class="text-caption text-medium-emphasis"
                >
                  {{ t("connect.source", { source: discovery.source }) }}
                </div>
              </div>

              <div
                v-if="oauthApps.length"
                class="d-flex flex-column ga-2 mb-4"
              >
                <v-btn
                  v-for="app in oauthApps"
                  :key="app.id"
                  :color="app.provider === 'gmail' ? 'error' : 'info'"
                  :loading="loading"
                  block
                  @click="onOauth(app)"
                >
                  {{ oauthLabel(app) }}
                </v-btn>
              </div>

              <div
                v-if="oauthApps.length"
                class="text-body-small text-medium-emphasis mb-2"
              >
                {{ t("connect.or_password") }}
              </div>

              <v-text-field
                v-model="displayName"
                :label="t('connect.name')"
                class="mb-2 no-validation"
              />
              <v-text-field
                v-model="password"
                :label="t('connect.password')"
                :rules="combine(required)"
                :prepend-inner-icon="mdiLockOutline"
                type="password"
                autocomplete="current-password"
                class="mb-2"
              />
              <v-btn
                :color="createAction.color"
                :loading="loading"
                block
                @click="onPassword"
              >
                {{ t("connect.connect") }}
              </v-btn>
            </template>
          </v-form>

          <div
            v-else
            class="text-center py-4"
          >
            <v-icon-hero
              :icon="mdiCheckCircle"
              color="success"
              class="mb-3"
            />
            <div class="text-title-medium font-weight-bold mb-1">
              {{ t("connect.success") }}
            </div>
            <div class="text-body-medium text-medium-emphasis">
              {{ doneRedirect ? t("connect.redirecting") : t("connect.success_body") }}
            </div>
          </div>
        </template>
      </template>
    </AppCard>
  </div>
</template>

<style scoped lang="scss">
.connect-page {
  min-height: 70vh;
}

.connect-card {
  width: 100%;
  max-width: 28rem;
}
</style>
