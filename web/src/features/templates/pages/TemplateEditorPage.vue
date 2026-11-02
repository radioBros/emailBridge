<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { mdiArrowLeft, mdiCodeTags, mdiFileDocumentOutline, mdiLanguageHtml5 } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import { useNotifyStore } from "@/stores/notify";
import { useTemplateEditor } from "@/features/templates/composables/useTemplateEditor";
import MjmlEditor from "@/features/templates/components/MjmlEditor.vue";
import type { HtmlFormat } from "@/features/templates/helpers/mjml";
import { sanitizeHtml } from "@/features/outbox/helpers/richText";

const RichTextEditor = defineAsyncComponent(
  () => import("@/features/outbox/components/RichTextEditor.vue"),
);

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const notify = useNotifyStore();
const saveAction = actionOf("save");
const createAction = actionOf("create");
const addAction = actionOf("add");
const removeAction = actionOf("remove");
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const mjmlEditor = ref<{ reveal: (line: number) => void } | null>(null);

const {
  form,
  current,
  loadError,
  formError,
  formFieldErrors,
  htmlErrors,
  compiledHtml,
  saving,
  sampleVars,
  previewVersion,
  previewResult,
  previewError,
  previewing,
  variableHints,
  versionOptions,
  isFirstLoad,
  isRefreshing,
  load,
  resetNew,
  save,
  runPreview,
  setHtmlFormat,
} = useTemplateEditor();

const isMjml = computed(() => form.value.htmlFormat === "mjml");

const htmlFormatModel = computed({
  get: (): HtmlFormat => form.value.htmlFormat,
  set: (next: HtmlFormat) => setHtmlFormat(next),
});

const revealLine = (line?: number): void => {
  if (line != null) mjmlEditor.value?.reveal(line);
};

const isNew = computed(() => route.name === "templates-new" || !route.params.templateId);

const title = computed(() =>
  isNew.value ? t("templates.editor_new") : t("templates.editor_edit"),
);

const versionSelectItems = computed(() =>
  versionOptions.value.map((n) => ({ title: t("templates.version_n", { n }), value: n })),
);

const previewErrorText = computed(() => {
  const e = previewError.value;
  if (!e) return null;
  if (e === "unsaved") return t("templates.preview_unsaved");
  if (e === "json") return t("templates.preview_json");
  if (e === "object") return t("templates.preview_object");
  return e;
});

const isCurrentVersion = (item: unknown): boolean => {
  const v = (item as { value?: number } | null)?.value;
  return v != null && v === current.value?.currentVersion;
};

const boot = async (): Promise<void> => {
  if (isNew.value) {
    resetNew();
    return;
  }
  const id = String(route.params.templateId ?? "");
  await load(id);
};

onMounted(() => {
  void boot();
});

watch(
  () => route.params.templateId,
  () => {
    void boot();
  },
);

const addPartial = (): void => {
  form.value.partials.push({ name: "", source: "" });
};

const removePartial = (idx: number): void => {
  form.value.partials.splice(idx, 1);
};

const onSave = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  // MJML is compiled (and validated) on the server; only the rich-text HTML is cleaned here.
  if (!isMjml.value) form.value.html = sanitizeHtml(form.value.html || "");
  const saved = await save();
  if (!saved) return;
  notify.push({ message: t("templates.saved"), color: "success" });
  if (isNew.value) {
    await router.replace({ name: "templates-edit", params: { templateId: saved.id } });
  }
};

const goBack = (): void => {
  void router.push({ name: "templates" });
};
</script>

<template>
  <div>
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-6">
      <div class="min-w-0">
        <v-btn
          :prepend-icon="mdiArrowLeft"
          :color="actionOf('navigate').color"
          class="mb-2"
          @click="goBack"
        >
          {{ t("templates.back") }}
        </v-btn>
        <h1 class="text-h5 font-weight-bold mb-1 d-flex align-center ga-2">
          <v-avatar
            color="success"
          >
            <v-icon
              :icon="mdiFileDocumentOutline"
            />
          </v-avatar>
          {{ title }}
        </h1>
      </div>
      <v-btn
        :color="isNew ? createAction.color : saveAction.color"
        :prepend-icon="isNew ? createAction.icon : saveAction.icon"
        :loading="saving"
        data-testid="templates-save"
        @click="onSave"
      >
        {{ t("templates.save") }}
      </v-btn>
    </div>

    <v-alert
      v-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
    </v-alert>

    <v-alert
      v-if="formError"
      type="error"
      class="mb-4"
    >
      {{ formError }}
    </v-alert>

    <LoadingSkeleton
      v-if="isFirstLoad && !isNew"
      :rows="10"
      shape="card"
    />

    <div
      v-else
      class="position-relative"
    >
      <AppSectionLoader :status="isRefreshing && !isNew" />

      <!-- Columns take their natural height (AppCard is height: 100% by default): the editor card and
           the side stack must not stretch each other. -->
      <v-row align="start">
        <v-col
          cols="12"
          lg="8"
        >
          <AppCard>
            <v-form
              ref="formRef"
              @submit.prevent="onSave"
            >
              <v-text-field
                v-model="form.name"
                :label="t('templates.field_name')"
                :rules="required"
                :error-messages="formFieldErrors.name ? [formFieldErrors.name] : []"
                class="mb-2"
                data-testid="templates-name"
              />

              <v-row>
                <v-col cols="12">
                  <v-switch
                    v-model="form.strict"
                    :label="t('templates.field_strict')"
                    class="no-validation"
                    color="primary"
                    hide-details
                  />
                  <p class="text-body-small text-medium-emphasis mt-1 mb-0">
                    {{ t("templates.field_strict_hint") }}
                  </p>
                </v-col>
                <v-col
                  cols="12"
                  md="6"
                >
                  <v-text-field
                    v-model="form.locale"
                    :label="t('templates.field_locale')"
                    class="no-validation"
                    hide-details
                  />
                </v-col>
                <v-col
                  cols="12"
                  md="6"
                >
                  <v-text-field
                    v-model="form.timezone"
                    :label="t('templates.field_timezone')"
                    class="no-validation"
                    hide-details
                  />
                </v-col>
              </v-row>

              <v-text-field
                v-model="form.subject"
                :label="t('templates.field_subject')"
                class="mt-4 no-validation"
                data-testid="templates-subject"
                hide-details
              />

              <v-textarea
                v-model="form.text"
                :label="t('templates.field_text')"
                class="mt-4 no-validation"
                data-testid="templates-text"
                rows="5"
                hide-details
              />

              <div
                class="mt-4"
                data-testid="templates-html"
              >
                <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-2">
                  <div class="min-w-0">
                    <div class="text-subtitle-1 font-weight-bold">
                      {{ t("templates.field_html") }}
                    </div>
                    <div class="text-body-small text-medium-emphasis">
                      {{ isMjml ? t("templates.format_mjml_hint") : t("templates.format_html_hint") }}
                    </div>
                  </div>
                  <v-btn-toggle
                    v-model="htmlFormatModel"
                    :aria-label="t('templates.format')"
                    color="primary"
                    data-testid="templates-html-format"
                    mandatory
                    divided
                  >
                    <v-btn
                      :prepend-icon="mdiLanguageHtml5"
                      value="html"
                      data-testid="templates-format-html"
                    >
                      {{ t("templates.format_html") }}
                    </v-btn>
                    <v-btn
                      :prepend-icon="mdiCodeTags"
                      value="mjml"
                      data-testid="templates-format-mjml"
                    >
                      {{ t("templates.format_mjml") }}
                    </v-btn>
                  </v-btn-toggle>
                </div>

                <template v-if="isMjml">
                  <v-alert
                    v-if="htmlErrors.length"
                    :title="t('templates.mjml_errors_title')"
                    type="error"
                    class="mb-3"
                    data-testid="templates-mjml-errors"
                  >
                    <ul class="pl-4 mt-1">
                      <li
                        v-for="(err, idx) in htmlErrors"
                        :key="idx"
                        class="text-body-small"
                      >
                        <button
                          v-if="err.line != null"
                          v-tippy="t('templates.mjml_error_goto', { line: err.line })"
                          type="button"
                          class="mjml-error-link text-left"
                          @click="revealLine(err.line)"
                        >
                          {{ err.message }}
                        </button>
                        <span v-else>{{ err.message }}</span>
                      </li>
                    </ul>
                  </v-alert>
                  <MjmlEditor
                    ref="mjmlEditor"
                    v-model="form.html"
                    :errors="htmlErrors"
                    :aria-label="t('templates.format_mjml')"
                    :placeholder="t('templates.mjml_placeholder')"
                  />
                  <v-expansion-panels
                    v-if="compiledHtml"
                    class="mt-3"
                    data-testid="templates-mjml-compiled"
                  >
                    <v-expansion-panel :title="t('templates.mjml_compiled')">
                      <v-expansion-panel-text>
                        <p class="text-body-small text-medium-emphasis mb-2">
                          {{ t("templates.mjml_compiled_hint") }}
                        </p>
                        <pre class="text-body-small pa-2 rounded bg-surface-light overflow-auto mjml-compiled">{{
                          compiledHtml
                        }}</pre>
                      </v-expansion-panel-text>
                    </v-expansion-panel>
                  </v-expansion-panels>
                </template>
                <Suspense v-else>
                  <RichTextEditor
                    v-model="form.html"
                    :label="t('templates.field_html')"
                  />
                  <template #fallback>
                    <v-skeleton-loader type="article" />
                  </template>
                </Suspense>
              </div>

              <div class="mt-6">
                <div class="d-flex align-center justify-space-between mb-2">
                  <div class="text-subtitle-1 font-weight-bold">
                    {{ t("templates.partials") }}
                  </div>
                  <v-btn-inline
                    :prepend-icon="addAction.icon"
                    :color="addAction.color"
                    @click="addPartial"
                  >
                    {{ t("templates.partial_add") }}
                  </v-btn-inline>
                </div>
                <div
                  v-for="(p, idx) in form.partials"
                  :key="idx"
                  class="mb-4 pa-3 rounded-lg bg-surface-light"
                >
                  <v-text-field
                    v-model="p.name"
                    :label="t('templates.partial_name')"
                    class="no-validation mb-2"
                    hide-details
                  />
                  <v-textarea
                    v-model="p.source"
                    :label="t('templates.partial_source')"
                    class="no-validation"
                    rows="3"
                    hide-details
                  />
                  <v-btn-inline
                    :color="removeAction.color"
                    class="mt-2"
                    @click="removePartial(idx)"
                  >
                    {{ t("templates.partial_remove") }}
                  </v-btn-inline>
                </div>
              </div>

              <div class="mt-4">
                <div class="text-body-small text-medium-emphasis mb-2">
                  {{ t("templates.hints") }}
                </div>
                <div
                  v-if="variableHints.length"
                  class="d-flex flex-wrap ga-2"
                >
                  <v-chip
                    v-for="h in variableHints"
                    :key="h"
                    color="info"
                  >
                    {{ h }}
                  </v-chip>
                </div>
                <div
                  v-else
                  class="text-body-small text-medium-emphasis"
                >
                  {{ t("templates.hints_empty") }}
                </div>
              </div>
            </v-form>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
          lg="4"
          class="d-flex flex-column ga-4"
        >
          <AppCard class="h-auto">
            <div class="text-subtitle-1 font-weight-bold mb-2">
              {{ t("templates.versions") }}
            </div>
            <p class="text-body-small text-medium-emphasis mb-3">
              {{ t("templates.versions_hint") }}
            </p>
            <AutocompleteSearch
              v-if="versionOptions.length"
              v-model="previewVersion"
              :items="versionSelectItems"
              :label="t('templates.versions')"
              class="no-validation"
              hide-details
            >
              <template #item="{ props: itemProps, item }">
                <v-list-item v-bind="itemProps">
                  <template #append>
                    <v-chip
                      v-if="isCurrentVersion(item)"
                      color="success"
                    >
                      {{ t("templates.version_current") }}
                    </v-chip>
                  </template>
                </v-list-item>
              </template>
            </AutocompleteSearch>
            <div
              v-else
              class="text-body-small text-medium-emphasis"
            >
              {{ t("templates.preview_unsaved") }}
            </div>
          </AppCard>

          <AppCard class="h-auto">
            <div class="text-subtitle-1 font-weight-bold mb-2">
              {{ t("templates.preview") }}
            </div>
            <v-textarea
              v-model="sampleVars"
              :label="t('templates.sample_vars')"
              :hint="t('templates.sample_vars_hint')"
              class="no-validation mb-3"
              rows="5"
              persistent-hint
            />
            <v-btn
              :loading="previewing"
              class="mb-4"
              color="info"
              data-testid="templates-preview"
              block
              @click="runPreview"
            >
              {{ t("templates.preview_run") }}
            </v-btn>

            <v-alert
              v-if="previewErrorText"
              type="error"
              class="mb-3"
            >
              {{ previewErrorText }}
            </v-alert>

            <template v-if="previewResult">
              <div class="text-caption text-medium-emphasis mb-1">
                {{ t("templates.preview_subject") }}
              </div>
              <div class="mb-3 text-body-medium">
                {{ previewResult.subject || "—" }}
              </div>
              <div class="text-caption text-medium-emphasis mb-1">
                {{ t("templates.preview_text") }}
              </div>
              <pre class="text-body-small mb-3 pa-2 rounded bg-surface-light overflow-auto">{{
                previewResult.text || "—"
              }}</pre>
              <div class="text-caption text-medium-emphasis mb-1">
                {{ t("templates.preview_html") }}
              </div>
              <iframe
                v-if="previewResult.html"
                :srcdoc="previewResult.html"
                :style="{ minHeight: isMjml ? '420px' : '160px', border: 0 }"
                class="w-100 rounded bg-surface-light"
                sandbox=""
                title="preview"
              />
              <div
                v-else
                class="text-body-small"
              >
                —
              </div>
            </template>
          </AppCard>
        </v-col>
      </v-row>
    </div>
  </div>
</template>

<style scoped lang="scss">
.mjml-error-link {
  color: inherit;
  text-decoration: underline;
  text-underline-offset: 3px;
  cursor: pointer;
}

.mjml-compiled {
  max-height: 320px;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
