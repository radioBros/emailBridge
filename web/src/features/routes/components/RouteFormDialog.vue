<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { EndpointView } from "@/api/webhooks";
import type {
  Route,
  RouteCreate,
  RouteUpdate,
  RuleBuilder,
} from "@/api/routes";
import { compileBuilder, expressibleBuilder } from "@/api/routes";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import CelEditor from "@/features/routes/components/CelEditor.vue";
import RuleBuilderPanel from "@/features/routes/components/RuleBuilderPanel.vue";
import AppDialog from "@/components/AppDialog.vue";


const props = defineProps<{
  modelValue: boolean;
  route: Route | null;
  endpoints: EndpointView[];
  submitting: boolean;
  errorMsg: string | null;
  fieldErrors: Record<string, string>;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [payload: RouteCreate | (RouteUpdate & { id: string })];
}>();

const { t } = useI18n();
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const saveAction = actionOf("save");

const emptyBuilder = (): RuleBuilder => ({
  match: "all",
  conditions: [{ field: "type", op: "eq", value: "message.received" }],
});

const form = reactive({
  name: "",
  endpointId: "",
  enabled: true,
  filter: 'type == "message.received"',
  map: "",
  builder: emptyBuilder() as RuleBuilder,
  mode: "builder" as "builder" | "cel",
  builderOk: true,
});

const isEdit = computed(() => Boolean(props.route));

const endpointItems = computed(() =>
  props.endpoints.map((e) => ({
    title: `${e.name} (${e.kind})`,
    value: e.id,
    subtitle: e.url ?? e.id,
  })),
);

const reset = (): void => {
  form.name = "";
  form.endpointId = props.endpoints[0]?.id ?? "";
  form.enabled = true;
  form.filter = 'type == "message.received"';
  form.map = "";
  form.builder = emptyBuilder();
  form.mode = "builder";
  form.builderOk = true;
};

const fill = async (r: Route): Promise<void> => {
  form.name = r.name;
  form.endpointId = r.endpointId;
  form.enabled = r.enabled;
  form.filter = r.filter || "";
  form.map = r.map || "";
  form.builder = emptyBuilder();
  form.builderOk = true;
  form.mode = "cel";
  try {
    const exp = await expressibleBuilder(form.filter);
    if (exp.expressible && exp.builder) {
      form.builder = exp.builder;
      form.builderOk = true;
      form.mode = "builder";
    } else if (r.builder && typeof r.builder === "object") {
      const b = r.builder as RuleBuilder;
      if (b.match && Array.isArray(b.conditions)) {
        form.builder = { match: b.match, conditions: b.conditions };
      }
      form.builderOk = false;
      form.mode = "cel";
    } else {
      form.builderOk = false;
      form.mode = "cel";
    }
  } catch {
    form.builderOk = false;
    form.mode = "cel";
  }
};

watch(
  () => [props.modelValue, props.route] as const,
  async ([open, r]) => {
    if (!open) return;
    if (r) await fill(r);
    else reset();
  },
);

const syncFilterFromBuilder = async (): Promise<boolean> => {
  try {
    const res = await compileBuilder(form.builder);
    if (!res.ok) return false;
    form.filter = res.cel;
    form.builderOk = true;
    return true;
  } catch {
    return false;
  }
};

const onMode = async (mode: "builder" | "cel"): Promise<void> => {
  if (mode === "builder") {
    if (!form.builderOk) return;
    form.mode = "builder";
    return;
  }
  if (form.mode === "builder") {
    await syncFilterFromBuilder();
  }
  form.mode = "cel";
};

const onCelEdit = async (): Promise<void> => {
  try {
    const exp = await expressibleBuilder(form.filter);
    form.builderOk = exp.expressible;
    if (exp.expressible && exp.builder) {
      form.builder = exp.builder;
    }
  } catch {
    form.builderOk = false;
  }
};

const onSubmit = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  if (form.mode === "builder") {
    const ok = await syncFilterFromBuilder();
    if (!ok) return;
  }
  const body: RouteCreate = {
    name: form.name.trim(),
    endpointId: form.endpointId,
    enabled: form.enabled,
    filter: form.filter,
    map: form.map.trim() || undefined,
    builder: form.builderOk ? form.builder : undefined,
  };
  if (props.route) {
    emit("submit", { id: props.route.id, ...body });
  } else {
    emit("submit", body);
  }
};

const close = (): void => {
  emit("update:modelValue", false);
};

const resetToBuilder = async (): Promise<void> => {
  form.builder = emptyBuilder();
  form.builderOk = true;
  await syncFilterFromBuilder();
  form.mode = "builder";
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="isEdit ? t('routes.form_title_edit') : t('routes.form_title_create')"
    :max-width="860"
    :loading="submitting"
    fullscreen-on-mobile
    @update:model-value="emit('update:modelValue', $event)"
  >
    <v-alert
      v-if="errorMsg"
      type="error"
      class="mb-4"
    >
      {{ errorMsg }}
    </v-alert>
    <v-form
      ref="formRef"
      @submit.prevent="onSubmit"
    >
      <v-row>
        <v-col
          cols="12"
          md="6"
        >
          <v-text-field
            v-model="form.name"
            :label="t('routes.field_name')"
            :rules="required"
            :error-messages="fieldErrors.name ? [fieldErrors.name] : []"
            autocomplete="off"
          />
        </v-col>
        <v-col
          cols="12"
          md="6"
        >
          <AutocompleteSearch
            v-model="form.endpointId"
            :items="endpointItems"
            :label="t('routes.field_endpoint')"
            :rules="required"
            :search-fields="['title', 'subtitle', 'value']"
          />
        </v-col>
        <v-col cols="12">
          <v-switch
            v-model="form.enabled"
            :label="t('routes.field_enabled')"
            color="success"
            class="no-validation"
            hide-details
          />
        </v-col>
        <v-col cols="12">
          <div class="d-flex flex-wrap align-center ga-2 mb-3">
            <v-btn-toggle
              :model-value="form.mode"
              color="accent"
              mandatory
              divided
              @update:model-value="(v) => onMode((v as 'builder' | 'cel') || 'cel')"
            >
              <v-btn
                :disabled="!form.builderOk && form.mode === 'cel'"
                value="builder"
              >
                {{ t("routes.mode_builder") }}
              </v-btn>
              <v-btn
                value="cel"
              >
                {{ t("routes.mode_cel") }}
              </v-btn>
            </v-btn-toggle>
            <v-btn-inline
              v-if="!form.builderOk"
              @click="resetToBuilder"
            >
              {{ t("routes.builder_reset") }}
            </v-btn-inline>
          </div>
          <v-alert
            v-if="!form.builderOk"
            type="warning"
            class="mb-3"
          >
            {{ t("routes.builder_advanced_notice") }}
          </v-alert>
          <RuleBuilderPanel
            v-if="form.mode === 'builder'"
            v-model="form.builder"
          />
          <CelEditor
            v-show="form.mode === 'cel'"
            v-model="form.filter"
            :label="t('routes.field_filter')"
            :hint="t('routes.field_filter_hint')"
            :rules="required"
            :rows="5"
            @update:model-value="onCelEdit"
          />
        </v-col>
        <v-col cols="12">
          <CelEditor
            v-model="form.map"
            :label="t('routes.field_map')"
            :hint="t('routes.field_map_hint')"
            :rows="5"
          />
        </v-col>
      </v-row>
    </v-form>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("routes.cancel") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="submitting"
        @click="onSubmit"
      >
        {{ t("routes.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
