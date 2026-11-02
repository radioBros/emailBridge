<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiLock } from "@mdi/js";
import type { FeatureSwitchBody } from "@/api/settings";
import { SOURCE_COLORS } from "@/features/settings/catalog";
import { useSwitchLabels } from "@/composables/useSwitchLabels";

/**
 * One feature switch with its control, used by the Switches tab and by the Settings search results.
 * The text parts are slots (with today's markup as default) so the search can highlight them.
 */
const props = defineProps<{
  sw: FeatureSwitchBody;
  modelValue: unknown;
  locked: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: unknown];
}>();

const { t, te } = useI18n();
const { switchLabel, switchDescription } = useSwitchLabels();

const label = computed(() => switchLabel(props.sw.name));

const description = computed(() => switchDescription(props.sw.name, props.sw.description));

const source = computed(() => props.sw.source || "default");

const sourceColor = computed(() => {
  const c = SOURCE_COLORS[source.value] ?? "grey";
  return c === "grey" ? undefined : c;
});

const sourceLabel = computed(() => {
  const key = `settings.source.${source.value}`;
  return te(key) ? t(key) : source.value;
});

const sourceHelp = computed(() => {
  const key = `settings.source_help.${source.value}`;
  return te(key) ? t(key) : sourceLabel.value;
});

const isBool = computed(
  () => typeof props.modelValue === "boolean" || typeof props.sw.default === "boolean",
);

const textValue = computed(() => (props.modelValue == null ? "" : String(props.modelValue)));
</script>

<template>
  <div class="d-flex flex-wrap align-center justify-space-between ga-3 py-3 border-b">
    <div class="flex-grow-1 min-w-0">
      <div class="d-flex align-center flex-wrap ga-2 mb-1">
        <span class="text-body-medium font-weight-medium">
          <slot
            :label="label"
            name="label"
          >
            {{ label }}
          </slot>
        </span>
        <v-chip
          v-tippy="sourceHelp"
          :color="sourceColor"
          label
        >
          {{ sourceLabel }}
        </v-chip>
        <v-icon-inline
          v-if="locked"
          :icon="mdiLock"
          class="text-medium-emphasis"
        />
        <slot name="meta" />
      </div>
      <code class="text-body-small text-medium-emphasis d-block mb-1">
        <slot
          :text="sw.name"
          name="name"
        >
          {{ sw.name }}
        </slot>
      </code>
      <div class="text-body-small text-medium-emphasis">
        <slot
          :description="description"
          name="description"
        >
          {{ description }}
        </slot>
      </div>
    </div>
    <v-switch
      v-if="isBool"
      :model-value="Boolean(modelValue)"
      :aria-label="label"
      :disabled="locked"
      color="success"
      class="no-validation flex-grow-0"
      hide-details
      @update:model-value="emit('update:modelValue', Boolean($event))"
    />
    <v-text-field
      v-else
      :model-value="textValue"
      :aria-label="label"
      :disabled="locked"
      class="no-validation switch-value-field"
      hide-details
      @update:model-value="emit('update:modelValue', $event)"
    />
  </div>
</template>

<style lang="scss" scoped>
.switch-value-field {
  max-width: 14rem;
}
</style>
