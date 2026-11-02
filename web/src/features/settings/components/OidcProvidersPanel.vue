<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiOpenid,
  mdiPencil,
  mdiPlus,
  mdiTrashCanOutline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import { useConfirm } from "@/composables/useConfirm";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ApiProblemError } from "@/api/client";
import { listIntegrations } from "@/api/integrations";
import {
  createOidcProvider,
  deleteOidcProvider,
  listOidcProviders,
  patchOidcProvider,
  type OidcProvider,
  type OidcProviderCreate,
} from "@/api/oidc";
import {
  parseOidcPreset,
  prefillFromOidcPreset,
  type OidcCataloguePreset,
} from "@/features/settings/helpers/oidcPreset";
import AppDialog from "@/components/AppDialog.vue";


const { t } = useI18n();
const confirm = useConfirm();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

const items = ref<OidcProvider[]>([]);
const presets = ref<OidcCataloguePreset[]>([]);
const loadError = ref<string | null>(null);
const forbidden = ref(false);
const mutating = ref(false);
const dialogOpen = ref(false);
const editing = ref<OidcProvider | null>(null);
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const selectedPresetId = ref<string | null>(null);
const presetHelp = ref("");

const issuerUrl = ref("");
const clientId = ref("");
const clientSecret = ref("");
const buttonLabel = ref("");
const scopesText = ref("openid, email, profile");
const domainsText = ref("");
const groupClaim = ref("");
const groupRolesText = ref("");
const autoProvision = ref(false);
const enabled = ref(true);

const createAction = actionOf("create");
const saveAction = actionOf("save");
const deleteAction = actionOf("delete");
const editAction = actionOf("edit");

const dialogTitle = computed(() =>
  editing.value ? t("settings.oidc.edit_title") : t("settings.oidc.create_title"),
);

const presetItems = computed(() =>
  presets.value.map((p) => ({
    title: p.name,
    value: p.id,
    subtitle: p.issuerUrlPattern,
  })),
);

const parseList = (raw: string): string[] =>
  raw
    .split(/[,;\n]/)
    .map((s) => s.trim())
    .filter(Boolean);

const parseGroupRoles = (raw: string): Record<string, string> | undefined => {
  const out: Record<string, string> = {};
  for (const line of raw.split(/[\n,]/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const i = trimmed.indexOf(":");
    if (i <= 0) continue;
    const g = trimmed.slice(0, i).trim();
    const r = trimmed.slice(i + 1).trim();
    if (g && r) out[g] = r;
  }
  return Object.keys(out).length ? out : undefined;
};

const formatGroupRoles = (m?: Record<string, string>): string => {
  if (!m) return "";
  return Object.entries(m)
    .map(([g, r]) => `${g}:${r}`)
    .join(", ");
};

const resetForm = (p?: OidcProvider | null): void => {
  editing.value = p ?? null;
  selectedPresetId.value = null;
  presetHelp.value = "";
  issuerUrl.value = p?.issuerUrl ?? "";
  clientId.value = p?.clientId ?? "";
  clientSecret.value = "";
  buttonLabel.value = p?.buttonLabel ?? "";
  scopesText.value = (p?.scopes ?? ["openid", "email", "profile"]).join(", ");
  domainsText.value = (p?.allowedEmailDomains ?? []).join(", ");
  groupClaim.value = p?.groupClaim ?? "";
  groupRolesText.value = formatGroupRoles(p?.groupRoles);
  autoProvision.value = p?.autoProvision ?? false;
  enabled.value = p?.enabled ?? true;
};

const applyPreset = (id: string | null): void => {
  if (!id) {
    presetHelp.value = "";
    return;
  }
  const preset = presets.value.find((p) => p.id === id);
  if (!preset) return;
  const filled = prefillFromOidcPreset(preset);
  issuerUrl.value = filled.issuerUrl;
  buttonLabel.value = filled.buttonLabel;
  scopesText.value = filled.scopesText;
  groupClaim.value = filled.groupClaim;
  presetHelp.value = filled.helpText;
};

watch(selectedPresetId, (id) => {
  applyPreset(id);
});

const load = async (): Promise<void> => {
  start();
  loadError.value = null;
  forbidden.value = false;
  try {
    const [oidc, cat] = await Promise.all([
      listOidcProviders(),
      listIntegrations({ category: "sso" }).catch(() => ({ data: [] })),
    ]);
    items.value = oidc;
    presets.value = (cat.data ?? [])
      .map((item) => parseOidcPreset(item))
      .filter((p): p is OidcCataloguePreset => p != null);
    finish(true);
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 403) {
      forbidden.value = true;
    } else {
      loadError.value = messageOf(err);
    }
    finish(false);
  }
};

onMounted(() => {
  void load();
});

const openCreate = (): void => {
  resetForm(null);
  dialogOpen.value = true;
};

const openEdit = (p: OidcProvider): void => {
  resetForm(p);
  dialogOpen.value = true;
};

const submit = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  mutating.value = true;
  try {
    if (editing.value) {
      await patchOidcProvider(editing.value.id, {
        issuerUrl: issuerUrl.value.trim(),
        clientId: clientId.value.trim(),
        ...(clientSecret.value.trim() ? { clientSecret: clientSecret.value.trim() } : {}),
        buttonLabel: buttonLabel.value.trim(),
        scopes: parseList(scopesText.value),
        allowedEmailDomains: parseList(domainsText.value),
        groupClaim: groupClaim.value.trim() || undefined,
        groupRoles: parseGroupRoles(groupRolesText.value),
        autoProvision: autoProvision.value,
        enabled: enabled.value,
      });
      notify.push({ message: t("settings.oidc.saved"), color: "success" });
    } else {
      const body: OidcProviderCreate = {
        issuerUrl: issuerUrl.value.trim(),
        clientId: clientId.value.trim(),
        clientSecret: clientSecret.value.trim(),
        buttonLabel: buttonLabel.value.trim(),
        scopes: parseList(scopesText.value),
        allowedEmailDomains: parseList(domainsText.value),
        groupClaim: groupClaim.value.trim() || undefined,
        groupRoles: parseGroupRoles(groupRolesText.value),
        autoProvision: autoProvision.value,
        enabled: enabled.value,
      };
      await createOidcProvider(body);
      notify.push({ message: t("settings.oidc.created"), color: "success" });
    }
    dialogOpen.value = false;
    await load();
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    mutating.value = false;
  }
};

const onDelete = async (p: OidcProvider): Promise<void> => {
  const ok = await confirm({
    title: t("settings.oidc.delete_title"),
    body: t("settings.oidc.delete_body", { label: p.buttonLabel }),
    variant: "danger",
    confirmText: t("settings.oidc.delete"),
  });
  if (!ok) return;
  mutating.value = true;
  try {
    await deleteOidcProvider(p.id);
    notify.push({ message: t("settings.oidc.deleted"), color: "success" });
    await load();
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    mutating.value = false;
  }
};
</script>

<template>
  <div class="position-relative">
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="4"
      shape="form"
    />

    <AppCard
      v-else-if="forbidden"
      class="text-center py-8"
    >
      <div class="text-title-medium mb-1">
        {{ t("settings.oidc.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("settings.oidc.no_permission_body") }}
      </p>
    </AppCard>

    <AppCard v-else>
      <template #header>
        <div class="d-flex flex-wrap align-center justify-space-between ga-3 w-100">
          <div class="d-flex align-center ga-3 min-w-0">
            <v-avatar
              color="info"
            >
              <v-icon
                :icon="mdiOpenid"
              />
            </v-avatar>
            <div class="min-w-0">
              <div class="text-title-medium">
                {{ t("settings.oidc.title") }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{ t("settings.oidc.subtitle") }}
              </div>
            </div>
          </div>
          <v-btn-inline
            :color="createAction.color"
            :prepend-icon="mdiPlus"
            data-testid="oidc-add"
            @click="openCreate"
          >
            {{ t("settings.oidc.add") }}
          </v-btn-inline>
        </div>
      </template>

      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load"
          >
            {{ t("settings.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <div
        v-if="!items.length && !loadError"
        class="text-body-medium text-medium-emphasis py-4"
      >
        {{ t("settings.oidc.empty") }}
      </div>

      <div
        v-for="p in items"
        :key="p.id"
        class="d-flex flex-wrap align-center justify-space-between ga-3 py-3 border-b"
      >
        <div class="min-w-0 flex-grow-1">
          <div class="d-flex align-center flex-wrap ga-2 mb-1">
            <span class="text-body-medium font-weight-medium">
              {{ p.buttonLabel }}
            </span>
            <v-chip
              :color="p.enabled ? 'success' : 'warning'"
              label
            >
              {{ p.enabled ? t("settings.oidc.enabled") : t("settings.oidc.disabled") }}
            </v-chip>
            <v-chip
              v-if="p.autoProvision"
              color="info"
              label
            >
              {{ t("settings.oidc.auto_provision") }}
            </v-chip>
          </div>
          <div class="text-body-small text-medium-emphasis text-truncate">
            {{ p.issuerUrl }}
          </div>
        </div>
        <div class="d-flex ga-1 flex-shrink-0">
          <v-btn-inline
            :icon="mdiPencil"
            :color="editAction.color"
            @click="openEdit(p)"
          />
          <v-btn-inline
            :icon="mdiTrashCanOutline"
            :color="deleteAction.color"
            @click="onDelete(p)"
          />
        </div>
      </div>
    </AppCard>

    <AppSectionLoader :status="isRefreshing || mutating" />

    <AppDialog
      v-model="dialogOpen"
      :title="dialogTitle"
      :max-width="640"
      :loading="mutating"
      data-testid="oidc-dialog"
      fullscreen-on-mobile
    >
      <v-form
        ref="formRef"
        @submit.prevent="submit"
      >
        <AutocompleteSearch
          v-if="!editing && presetItems.length"
          v-model="selectedPresetId"
          :items="presetItems"
          :label="t('settings.oidc.preset_label')"
          :hint="t('settings.oidc.preset_hint')"
          class="mb-3 no-validation"
          data-testid="oidc-preset"
          clearable
          persistent-hint
        />
        <v-alert
          v-if="presetHelp"
          type="info"
          class="mb-3 text-body-small"
          data-testid="oidc-preset-help"
        >
          {{ presetHelp }}
        </v-alert>
        <v-text-field
          v-model="buttonLabel"
          :label="t('settings.oidc.field_label')"
          :rules="required"
          class="mb-2"
        />
        <v-text-field
          v-model="issuerUrl"
          :label="t('settings.oidc.field_issuer')"
          :rules="required"
          class="mb-2"
          data-testid="oidc-issuer"
        />
        <v-text-field
          v-model="clientId"
          :label="t('settings.oidc.field_client_id')"
          :rules="required"
          class="mb-2"
        />
        <v-text-field
          v-model="clientSecret"
          :label="editing ? t('settings.oidc.field_secret_optional') : t('settings.oidc.field_secret')"
          :rules="editing ? undefined : required"
          :class="editing ? 'mb-2 no-validation' : 'mb-2'"
          type="password"
          autocomplete="new-password"
        />
        <v-text-field
          v-model="scopesText"
          :label="t('settings.oidc.field_scopes')"
          :hint="t('settings.strings_hint')"
          class="mb-2 no-validation"
          data-testid="oidc-scopes"
          persistent-hint
        />
        <v-text-field
          v-model="domainsText"
          :label="t('settings.oidc.field_domains')"
          :hint="t('settings.oidc.field_domains_hint')"
          class="mb-2 no-validation"
          persistent-hint
        />
        <v-text-field
          v-model="groupClaim"
          :label="t('settings.oidc.field_group_claim')"
          class="mb-2 no-validation"
          data-testid="oidc-group-claim"
        />
        <v-text-field
          v-model="groupRolesText"
          :label="t('settings.oidc.field_group_roles')"
          :hint="t('settings.oidc.field_group_roles_hint')"
          class="mb-2 no-validation"
          persistent-hint
        />
        <div class="d-flex flex-wrap ga-6">
          <v-switch
            v-model="enabled"
            :label="t('settings.oidc.field_enabled')"
            color="success"
            class="no-validation"
            hide-details
          />
          <v-switch
            v-model="autoProvision"
            :label="t('settings.oidc.field_auto_provision')"
            color="info"
            class="no-validation"
            hide-details
          />
        </div>
      </v-form>

      <template #footer>
        <v-btn
          @click="dialogOpen = false"
        >
          {{ t("settings.discard") }}
        </v-btn>
        <v-btn
          :color="saveAction.color"
          :loading="mutating"
          @click="submit"
        >
          {{ editing ? t("settings.save") : t("settings.oidc.add") }}
        </v-btn>
      </template>
    </AppDialog>
  </div>
</template>
