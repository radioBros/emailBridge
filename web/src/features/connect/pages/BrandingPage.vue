<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import {
  mdiCodeTags,
  mdiContentCopy,
  mdiEyeOutline,
  mdiImageMultipleOutline,
  mdiPaletteOutline,
  mdiRestore,
  mdiShieldLock,
  mdiStorefrontOutline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import BrandAssetUpload from "@/components/BrandAssetUpload.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import ColourField from "@/features/connect/components/ColourField.vue";
import { actionOf } from "@/constants/Actions";
import { useLoadState } from "@/composables/useLoadState";
import { useBrandTheme } from "@/composables/useBrandTheme";
import { useNotifyStore } from "@/stores/notify";
import { contrast, isHexColor } from "@/utils/contrast";
import { dark as defaultDark, light as defaultLight } from "@/core/config/VuetifyThemes";
import {
  getConnectBranding,
  listBrandingAssets,
  patchConnectBranding,
  previewConnectBranding,
  publicBrandingAssetPath,
  type BrandingAsset,
  type BrandingPalette,
  type ConnectBranding,
} from "@/api/connect";
import { ApiProblemError } from "@/api/client";

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const notify = useNotifyStore();
const { applyPalette } = useBrandTheme();
const saveAction = actionOf("save");
const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

const BRANDING_TABS = new Set(["identity", "colours", "html", "images"]);
const readTab = (raw: unknown): string => {
  const v = typeof raw === "string" ? raw : Array.isArray(raw) ? String(raw[0] ?? "") : "";
  return BRANDING_TABS.has(v) ? v : "identity";
};
const tab = ref(readTab(route.query.tab));

watch(
  () => route.query.tab,
  (v) => {
    const next = readTab(v);
    if (next !== tab.value) tab.value = next;
  },
);

watch(tab, (v) => {
  if (readTab(route.query.tab) === v) return;
  const query = { ...route.query } as Record<string, string | string[] | undefined>;
  if (v === "identity") delete query.tab;
  else query.tab = v;
  void router.replace({ query });
});

const brandName = ref("");
const logoAssetId = ref<string | null>(null);
const logoDarkAssetId = ref<string | null>(null);
const faviconAssetId = ref<string | null>(null);
const logoUrl = ref<string | null>(null);
const logoDarkUrl = ref<string | null>(null);
const faviconUrl = ref<string | null>(null);
const headerHtml = ref("");
const headHtml = ref("");
const returnUrlHosts = ref<string[]>([]);
const etag = ref<string | undefined>();
const forbidden = ref(false);
const loadError = ref(false);
const saving = ref(false);
const previewHeader = ref("");
const previewHead = ref("");
const previewing = ref(false);
const paletteMode = ref<"light" | "dark">("light");
const imageAssets = ref<BrandingAsset[]>([]);
const palette = reactive<{ light: Record<string, string>; dark: Record<string, string> }>({
  light: {},
  dark: {},
});

const COLOUR_GROUPS: { titleKey: string; keys: string[] }[] = [
  {
    titleKey: "connect.branding.palette_brand",
    keys: ["primary", "on-primary", "secondary", "on-secondary", "accent", "on-accent"],
  },
  {
    titleKey: "connect.branding.palette_status",
    keys: [
      "success",
      "on-success",
      "info",
      "on-info",
      "warning",
      "on-warning",
      "error",
      "on-error",
    ],
  },
  {
    titleKey: "connect.branding.palette_surfaces",
    keys: [
      "background",
      "on-background",
      "surface",
      "on-surface",
      "surface-variant",
      "on-surface-variant",
      "surface-bright",
      "field",
      "skeleton",
      "skeleton-highlight",
    ],
  },
  {
    titleKey: "connect.branding.palette_icons",
    keys: [
      "icon-indigo",
      "icon-cyan",
      "icon-violet",
      "icon-teal",
      "icon-amber",
      "icon-rose",
    ],
  },
];

const defaultsFor = (mode: "light" | "dark"): Record<string, string> =>
  ((mode === "light" ? defaultLight : defaultDark).colors ?? {}) as Record<string, string>;

const colourValue = (key: string): string => {
  const overrides = palette[paletteMode.value];
  return overrides[key] || defaultsFor(paletteMode.value)[key] || "#000000";
};

const setColour = (key: string, value: string): void => {
  if (!isHexColor(value)) return;
  palette[paletteMode.value][key] = value.toUpperCase();
};

const resetColour = (key: string): void => {
  delete palette[paletteMode.value][key];
};

const resetAllColours = (): void => {
  palette.light = {};
  palette.dark = {};
};

const contrastWarnings = computed(() => {
  const colors = {
    ...defaultsFor(paletteMode.value),
    ...palette[paletteMode.value],
  };
  const warnings: string[] = [];
  const check = (fg: string, bg: string) => {
    if (!colors[fg] || !colors[bg]) return;
    if (contrast(colors[fg], colors[bg]) < 4.5) {
      warnings.push(`${fg} / ${bg}`);
    }
  };
  check("on-surface", "surface");
  check("on-background", "background");
  for (const k of ["primary", "secondary", "accent", "success", "info", "warning", "error"]) {
    check(`on-${k}`, k);
  }
  return warnings;
});

const previewAccent = computed(
  () => palette.light.primary || defaultsFor("light").primary || "#4338CA",
);

const previewSrcdoc = computed(() => {
  const accent = previewAccent.value;
  const logo = logoUrl.value
    ? `<img src="${logoUrl.value}" alt="" style="max-height:40px;margin-bottom:.75rem">`
    : "";
  return `<!doctype html><html><head><meta charset="utf-8">${previewHead.value}
<style>*,*::before,*::after{box-sizing:border-box}html,body{overflow-x:hidden}body{font-family:system-ui,sans-serif;margin:0;padding:1rem;color:#1c1917;background:#fafaf9}
.brand{color:${accent};font-weight:700;margin-bottom:.75rem}</style></head>
<body>${logo}<div class="brand">${brandName.value || "EmailBridge"}</div>
${previewHeader.value}
<form><label>Email<br><input disabled style="width:100%;padding:.5rem;margin:.35rem 0 1rem;border:1px solid #d6d3d1;border-radius:8px"></label>
<button disabled style="background:${accent};color:#fff;border:0;border-radius:8px;padding:.5rem 1rem">Continue</button></form>
</body></html>`;
});

const load = async (): Promise<void> => {
  start();
  loadError.value = false;
  forbidden.value = false;
  try {
    const res = await getConnectBranding();
    apply(res.data);
    etag.value = res.etag;
    imageAssets.value = await listBrandingAssets("image");
    await refreshPreview();
    finish(true);
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 403) {
      forbidden.value = true;
    } else {
      loadError.value = true;
    }
    finish(false);
  }
};

const hostOk = (raw: string): boolean => {
  const h = raw.trim().toLowerCase();
  if (!h || h.length > 253) return false;
  if (h.includes("://") || h.includes("/") || h.includes(":") || h.includes("*")) return false;
  return /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/.test(h);
};

const onReturnUrlHosts = (v: unknown): void => {
  const list = (Array.isArray(v) ? v : [])
    .map((x) => String(x).trim().toLowerCase())
    .filter(Boolean);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const h of list) {
    if (!hostOk(h) || seen.has(h)) continue;
    seen.add(h);
    out.push(h);
    if (out.length >= 20) break;
  }
  returnUrlHosts.value = out;
};

const apply = (b: ConnectBranding): void => {
  brandName.value = b.brandName ?? "";
  logoAssetId.value = b.logoAssetId ?? null;
  logoDarkAssetId.value = b.logoDarkAssetId ?? null;
  faviconAssetId.value = b.faviconAssetId ?? null;
  logoUrl.value = b.logoUrl ?? null;
  logoDarkUrl.value = b.logoDarkUrl ?? null;
  faviconUrl.value = b.faviconUrl ?? null;
  headerHtml.value = b.headerHtml ?? "";
  headHtml.value = b.headHtml ?? "";
  returnUrlHosts.value = [...(b.returnUrlHosts ?? [])];
  previewHeader.value = b.headerHtml ?? "";
  previewHead.value = b.headHtml ?? "";
  palette.light = { ...(b.palette?.light ?? {}) };
  palette.dark = { ...(b.palette?.dark ?? {}) };
  if (b.primaryColor && !palette.light.primary) {
    palette.light.primary = b.primaryColor;
  }
};

const refreshPreview = async (): Promise<void> => {
  previewing.value = true;
  try {
    const prev = await previewConnectBranding({
      headerHtml: headerHtml.value,
      headHtml: headHtml.value,
    });
    previewHeader.value = prev.headerHtml ?? "";
    previewHead.value = prev.headHtml ?? "";
  } catch {
    previewHeader.value = "";
    previewHead.value = "";
  } finally {
    previewing.value = false;
  }
};

let previewTimer: ReturnType<typeof setTimeout> | undefined;
watch([headerHtml, headHtml], () => {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(() => {
    void refreshPreview();
  }, 400);
});

onMounted(() => {
  void load();
});

const palettePayload = (): BrandingPalette => ({
  light: { ...palette.light },
  dark: { ...palette.dark },
});

const save = async (): Promise<void> => {
  saving.value = true;
  try {
    const res = await patchConnectBranding(
      {
        brandName: brandName.value,
        logoAssetId: logoAssetId.value,
        logoDarkAssetId: logoDarkAssetId.value,
        faviconAssetId: faviconAssetId.value,
        primaryColor: palette.light.primary || undefined,
        palette: palettePayload(),
        headerHtml: headerHtml.value,
        headHtml: headHtml.value,
        returnUrlHosts: returnUrlHosts.value,
      },
      etag.value,
    );
    apply(res.data);
    etag.value = res.etag;
    applyPalette(res.data.palette ?? palettePayload());
    notify.push({ message: t("connect.branding.saved"), color: "success" });
  } catch (err) {
    const msg =
      err instanceof ApiProblemError ? err.problem.title : t("connect.branding.save_error");
    notify.push({ message: msg || t("connect.branding.save_error"), color: "error" });
  } finally {
    saving.value = false;
  }
};

const onImageUploaded = (asset: BrandingAsset): void => {
  imageAssets.value = [asset, ...imageAssets.value.filter((a) => a.id !== asset.id)];
};

const copyUrl = async (url: string): Promise<void> => {
  try {
    await navigator.clipboard.writeText(url);
    notify.push({ message: t("connect.branding.copied"), color: "success" });
  } catch {
    notify.push({ message: t("connect.branding.copy_error"), color: "error" });
  }
};

const isDarkPreview = computed(() => paletteMode.value === "dark");
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-start justify-space-between ga-3 mb-6">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("connect.branding.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("connect.branding.subtitle") }}
        </p>
      </div>
      <v-btn
        v-if="!forbidden && !isFirstLoad"
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="saving"
        @click="save"
      >
        {{ t("connect.branding.save") }}
      </v-btn>
    </div>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="10"
      shape="form"
    />

    <AppCard
      v-else-if="forbidden"
      class="text-center py-8"
    >
      <v-avatar
        color="warning"
        class="mb-4"
      >
        <v-icon :icon="mdiShieldLock" />
      </v-avatar>
      <div class="text-title-medium mb-1">
        {{ t("connect.branding.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("connect.branding.no_permission_body") }}
      </p>
    </AppCard>

    <AppCard
      v-else-if="loadError"
      class="text-center py-8"
    >
      <p class="text-body-medium mb-4">
        {{ t("connect.branding.load_error") }}
      </p>
      <v-btn
        :color="actionOf('retry').color"
        @click="load"
      >
        {{ t("connect.retry") }}
      </v-btn>
    </AppCard>

    <div
      v-else
      class="position-relative"
    >
      <AppSectionLoader :active="isRefreshing" />
      <v-row>
        <v-col
          cols="12"
          md="7"
        >
          <AppCard>
            <v-tabs
              v-model="tab"
              color="primary"
              class="mb-4"
              show-arrows
            >
              <v-tab value="identity">
                <v-icon
                  :icon="mdiStorefrontOutline"
                  color="icon-rose"
                  start
                />
                {{ t("connect.branding.tab_identity") }}
              </v-tab>
              <v-tab value="colours">
                <v-icon
                  :icon="mdiPaletteOutline"
                  color="icon-violet"
                  start
                />
                {{ t("connect.branding.tab_colours") }}
              </v-tab>
              <v-tab value="html">
                <v-icon
                  :icon="mdiCodeTags"
                  color="icon-teal"
                  start
                />
                {{ t("connect.branding.tab_html") }}
              </v-tab>
              <v-tab value="images">
                <v-icon
                  :icon="mdiImageMultipleOutline"
                  color="icon-cyan"
                  start
                />
                {{ t("connect.branding.tab_images") }}
              </v-tab>
            </v-tabs>

            <v-window v-model="tab">
              <v-window-item value="identity">
                <v-text-field
                  v-model="brandName"
                  :label="t('connect.branding.brand_name')"
                  class="no-validation mb-4"
                  hide-details="auto"
                />
                <v-row>
                  <v-col
                    cols="12"
                    sm="4"
                  >
                    <BrandAssetUpload
                      v-model:asset-id="logoAssetId"
                      v-model:preview-url="logoUrl"
                      :label="t('connect.branding.logo')"
                      :hint="t('connect.branding.logo_hint')"
                      :delete-on-remove="false"
                      kind="logo"
                    />
                  </v-col>
                  <v-col
                    cols="12"
                    sm="4"
                  >
                    <BrandAssetUpload
                      v-model:asset-id="logoDarkAssetId"
                      v-model:preview-url="logoDarkUrl"
                      :label="t('connect.branding.logo_dark')"
                      :hint="t('connect.branding.logo_dark_hint')"
                      :delete-on-remove="false"
                      kind="logoDark"
                    />
                  </v-col>
                  <v-col
                    cols="12"
                    sm="4"
                  >
                    <BrandAssetUpload
                      v-model:asset-id="faviconAssetId"
                      v-model:preview-url="faviconUrl"
                      :label="t('connect.branding.favicon')"
                      :hint="t('connect.branding.favicon_hint')"
                      :delete-on-remove="false"
                      kind="favicon"
                    />
                  </v-col>
                </v-row>
              </v-window-item>

              <v-window-item value="colours">
                <div class="d-flex align-center flex-wrap ga-3 mb-4">
                  <v-btn-toggle
                    v-model="paletteMode"
                    mandatory
                    divided
                  >
                    <v-btn value="light">
                      {{ t("connect.branding.palette_light") }}
                    </v-btn>
                    <v-btn value="dark">
                      {{ t("connect.branding.palette_dark") }}
                    </v-btn>
                  </v-btn-toggle>
                  <v-btn
                    :prepend-icon="mdiRestore"
                    class="ms-md-auto"
                    @click="resetAllColours"
                  >
                    {{ t("connect.branding.reset_palette") }}
                  </v-btn>
                </div>
                <p class="text-body-small text-medium-emphasis mb-4">
                  {{ t("connect.branding.palette_hint") }}
                </p>
                <v-alert
                  v-if="contrastWarnings.length"
                  class="mb-4"
                  type="warning"
                  role="status"
                >
                  {{ t("connect.branding.contrast_warn") }}:
                  {{ contrastWarnings.join(", ") }}
                </v-alert>
                <div
                  v-for="group in COLOUR_GROUPS"
                  :key="group.titleKey"
                  class="mb-5"
                >
                  <div class="text-label-large text-medium-emphasis mb-2">
                    {{ t(group.titleKey) }}
                  </div>
                  <v-row dense>
                    <v-col
                      v-for="key in group.keys"
                      :key="key"
                      cols="12"
                      sm="6"
                    >
                      <ColourField
                        :colour-key="key"
                        :model-value="colourValue(key)"
                        @update:model-value="(v) => setColour(key, v)"
                        @reset="resetColour(key)"
                      />
                    </v-col>
                  </v-row>
                </div>
              </v-window-item>

              <v-window-item value="html">
                <v-textarea
                  v-model="headerHtml"
                  :label="t('connect.branding.header_html')"
                  :hint="t('connect.branding.header_html_hint')"
                  class="no-validation mb-4"
                  rows="5"
                  persistent-hint
                  auto-grow
                />
                <v-textarea
                  v-model="headHtml"
                  :label="t('connect.branding.head_html')"
                  :hint="t('connect.branding.head_html_hint')"
                  class="no-validation mb-4"
                  rows="4"
                  persistent-hint
                  auto-grow
                />
                <v-combobox
                  :model-value="returnUrlHosts"
                  :label="t('connect.branding.return_url_hosts')"
                  :hint="t('connect.branding.return_url_hosts_hint')"
                  class="no-validation"
                  data-testid="branding-return-url-hosts"
                  multiple
                  chips
                  closable-chips
                  persistent-hint
                  @update:model-value="onReturnUrlHosts"
                />
              </v-window-item>

              <v-window-item value="images">
                <p class="text-body-small text-medium-emphasis mb-3">
                  {{ t("connect.branding.images_hint") }}
                </p>
                <BrandAssetUpload
                  :label="t('connect.branding.upload_image')"
                  :hint="t('connect.branding.logo_hint')"
                  kind="image"
                  class="mb-4"
                  @uploaded="onImageUploaded"
                />
                <div
                  v-if="!imageAssets.length"
                  class="text-body-medium text-medium-emphasis"
                >
                  {{ t("connect.branding.images_empty") }}
                </div>
                <v-list
                  v-else
                  lines="two"
                >
                  <v-list-item
                    v-for="asset in imageAssets"
                    :key="asset.id"
                  >
                    <template #prepend>
                      <v-avatar rounded>
                        <v-img
                          :src="asset.url"
                          alt=""
                        />
                      </v-avatar>
                    </template>
                    <v-list-item-title>{{ asset.id }}</v-list-item-title>
                    <v-list-item-subtitle>
                      {{ publicBrandingAssetPath(asset.id) }}
                    </v-list-item-subtitle>
                    <template #append>
                      <v-btn
                        :icon="mdiContentCopy"
                        :aria-label="t('connect.branding.copy_url')"
                        @click="copyUrl(publicBrandingAssetPath(asset.id))"
                      />
                    </template>
                  </v-list-item>
                </v-list>
              </v-window-item>
            </v-window>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
          md="5"
        >
          <div class="branding-preview-sticky">
            <AppCard class="mb-4">
              <template #header>
                <div class="d-flex align-center ga-2">
                  <v-icon
                    :icon="mdiEyeOutline"
                    color="icon-amber"
                  />
                  <span class="text-title-medium font-weight-medium">
                    {{ t("connect.branding.preview") }}
                  </span>
                  <v-progress-circular
                    v-if="previewing"
                    class="ms-auto"
                    indeterminate
                  />
                </div>
              </template>
              <iframe
                :srcdoc="previewSrcdoc"
                class="branding-preview"
                title="Hosted page preview"
                sandbox=""
              />
              <p class="text-body-small text-medium-emphasis mt-3 mb-0">
                {{ t("connect.branding.preview_note") }}
              </p>
            </AppCard>

            <AppCard>
              <template #header>
                <div class="d-flex align-center ga-2">
                  <v-icon
                    :icon="mdiPaletteOutline"
                    color="icon-violet"
                  />
                  <span class="text-title-medium font-weight-medium">
                    {{ t("connect.branding.palette_preview") }}
                  </span>
                </div>
              </template>
              <div
                :class="isDarkPreview ? 'palette-live--dark' : 'palette-live--light'"
                class="palette-live pa-4 rounded-lg"
              >
                <div class="d-flex flex-wrap ga-2 mb-3">
                  <v-btn
                    v-for="c in ['primary', 'secondary', 'success', 'error']"
                    :key="c"
                    :color="c"
                  >
                    {{ c }}
                  </v-btn>
                </div>
                <div class="d-flex flex-wrap ga-2 mb-3">
                  <v-chip
                    v-for="c in ['info', 'warning', 'accent']"
                    :key="c"
                    :color="c"
                  >
                    {{ c }}
                  </v-chip>
                </div>
                <v-alert
                  class="mb-3"
                  type="info"
                >
                  {{ t("connect.branding.palette_alert_sample") }}
                </v-alert>
                <v-text-field
                  :label="t('connect.branding.palette_field_sample')"
                  class="no-validation mb-3"
                  model-value="sample@example.test"
                  hide-details
                />
                <v-table>
                  <tbody>
                    <tr>
                      <td>{{ t("connect.branding.palette_row_sample") }}</td>
                      <td>
                        <v-chip color="success">
                          ok
                        </v-chip>
                      </td>
                    </tr>
                  </tbody>
                </v-table>
              </div>
            </AppCard>
          </div>
        </v-col>
      </v-row>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.branding-preview-sticky {
  position: sticky;
  top: 80px;
}

.branding-preview {
  display: block;
  width: 100%;
  min-height: 280px;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  background: rgb(var(--v-theme-surface));
}

.palette-live {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));

  &--light {
    background: #f5f6fb;
    color: #1e2433;
  }

  &--dark {
    background: #121933;
    color: #e8ecf8;
  }
}
</style>
