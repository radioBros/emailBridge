<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from "vue";
import { useI18n } from "vue-i18n";
import { EditorContent, useEditor } from "@tiptap/vue-3";
import StarterKit from "@tiptap/starter-kit";
import TextAlign from "@tiptap/extension-text-align";
import { Color, TextStyle } from "@tiptap/extension-text-style";
import Highlight from "@tiptap/extension-highlight";
import Image from "@tiptap/extension-image";
import { Table, TableCell, TableHeader, TableRow } from "@tiptap/extension-table";
import { TaskItem, TaskList } from "@tiptap/extension-list";
import { CharacterCount, Placeholder } from "@tiptap/extensions";
import Typography from "@tiptap/extension-typography";
import {
  mdiCodeTags,
  mdiFormatAlignCenter,
  mdiFormatAlignLeft,
  mdiFormatAlignRight,
  mdiFormatBold,
  mdiFormatColorHighlight,
  mdiFormatItalic,
  mdiFormatListBulleted,
  mdiFormatListChecks,
  mdiFormatListNumbered,
  mdiFormatUnderline,
  mdiImageOutline,
  mdiLinkVariant,
  mdiRedo,
  mdiTable,
  mdiUndo,
} from "@mdi/js";
import { actionOf } from "@/constants/Actions";
import { sanitizeHtml } from "@/features/outbox/helpers/richText";

const model = defineModel<string>({ default: "<p></p>" });
const props = withDefaults(
  defineProps<{
    label?: string;
    placeholder?: string;
    disabled?: boolean;
    /** Max characters (CharacterCount); 0 = unlimited. */
    maxLength?: number;
  }>(),
  { label: "", placeholder: "", disabled: false, maxLength: 0 },
);

const { t } = useI18n();
const sourceMode = defineModel<boolean>("source", { default: false });
const sourceText = computed({
  get: () => model.value,
  set: (v: string) => {
    model.value = v;
  },
});

const editor = useEditor({
  content: model.value || "<p></p>",
  editable: !props.disabled,
  extensions: [
    StarterKit.configure({
      heading: { levels: [1, 2, 3] },
      // Keep our link attrs; StarterKit ships Link + Underline in v3.
      link: {
        openOnClick: false,
        HTMLAttributes: { rel: "noopener noreferrer", target: "_blank" },
      },
    }),
    TextStyle,
    Color,
    Highlight.configure({ multicolor: true }),
    TextAlign.configure({ types: ["heading", "paragraph"] }),
    Image.configure({ allowBase64: false }),
    Table.configure({ resizable: false }),
    TableRow,
    TableHeader,
    TableCell,
    TaskList,
    TaskItem.configure({ nested: true }),
    Placeholder.configure({
      placeholder: () => props.placeholder || t("outbox.compose.editor_placeholder"),
    }),
    CharacterCount.configure({
      limit: props.maxLength > 0 ? props.maxLength : undefined,
    }),
    Typography,
  ],
  editorProps: {
    attributes: {
      class: "rich-text-editor__content",
      "aria-label": props.label || t("outbox.compose.html"),
      role: "textbox",
      "aria-multiline": "true",
    },
    transformPastedHTML: (html) => sanitizeHtml(html),
  },
  onUpdate: ({ editor: ed }) => {
    if (!sourceMode.value) {
      model.value = ed.getHTML();
    }
  },
});

watch(
  () => props.disabled,
  (v) => editor.value?.setEditable(!v),
);

watch(model, (v) => {
  const ed = editor.value;
  if (!ed || sourceMode.value) return;
  const next = v || "<p></p>";
  if (ed.getHTML() === next) return;
  ed.commands.setContent(next, { emitUpdate: false });
});

watch(sourceMode, (on) => {
  const ed = editor.value;
  if (!ed) return;
  if (on) {
    model.value = ed.getHTML();
  } else {
    ed.commands.setContent(sanitizeHtml(model.value || "<p></p>"), { emitUpdate: false });
  }
});

onBeforeUnmount(() => {
  editor.value?.destroy();
});

const chars = computed(() => editor.value?.storage.characterCount?.characters?.() ?? 0);

const run = (fn: () => boolean): void => {
  fn();
};

const askLink = (): void => {
  const ed = editor.value;
  if (!ed) return;
  const prev = ed.getAttributes("link").href as string | undefined;
  const url = window.prompt(t("outbox.compose.link_prompt"), prev || "https://");
  if (url === null) return;
  if (!url.trim()) {
    ed.chain().focus().extendMarkRange("link").unsetLink().run();
    return;
  }
  ed.chain().focus().extendMarkRange("link").setLink({ href: url.trim() }).run();
};

const askImage = (): void => {
  const ed = editor.value;
  if (!ed) return;
  const url = window.prompt(t("outbox.compose.image_prompt"), "https://");
  if (!url?.trim()) return;
  ed.chain().focus().setImage({ src: url.trim() }).run();
};

const insertTable = (): void => {
  editor.value
    ?.chain()
    .focus()
    .insertTable({ rows: 3, cols: 3, withHeaderRow: true })
    .run();
};

const toolbar = computed(() => [
  {
    id: "bold",
    icon: mdiFormatBold,
    label: t("outbox.compose.fmt_bold"),
    active: () => !!editor.value?.isActive("bold"),
    run: () => !!editor.value?.chain().focus().toggleBold().run(),
  },
  {
    id: "italic",
    icon: mdiFormatItalic,
    label: t("outbox.compose.fmt_italic"),
    active: () => !!editor.value?.isActive("italic"),
    run: () => !!editor.value?.chain().focus().toggleItalic().run(),
  },
  {
    id: "underline",
    icon: mdiFormatUnderline,
    label: t("outbox.compose.fmt_underline"),
    active: () => !!editor.value?.isActive("underline"),
    run: () => !!editor.value?.chain().focus().toggleUnderline().run(),
  },
  {
    id: "highlight",
    icon: mdiFormatColorHighlight,
    label: t("outbox.compose.fmt_highlight"),
    active: () => !!editor.value?.isActive("highlight"),
    run: () => !!editor.value?.chain().focus().toggleHighlight().run(),
  },
  {
    id: "bullet",
    icon: mdiFormatListBulleted,
    label: t("outbox.compose.fmt_bullet"),
    active: () => !!editor.value?.isActive("bulletList"),
    run: () => !!editor.value?.chain().focus().toggleBulletList().run(),
  },
  {
    id: "ordered",
    icon: mdiFormatListNumbered,
    label: t("outbox.compose.fmt_ordered"),
    active: () => !!editor.value?.isActive("orderedList"),
    run: () => !!editor.value?.chain().focus().toggleOrderedList().run(),
  },
  {
    id: "task",
    icon: mdiFormatListChecks,
    label: t("outbox.compose.fmt_task"),
    active: () => !!editor.value?.isActive("taskList"),
    run: () => !!editor.value?.chain().focus().toggleTaskList().run(),
  },
  {
    id: "left",
    icon: mdiFormatAlignLeft,
    label: t("outbox.compose.fmt_align_left"),
    active: () => !!editor.value?.isActive({ textAlign: "left" }),
    run: () => !!editor.value?.chain().focus().setTextAlign("left").run(),
  },
  {
    id: "center",
    icon: mdiFormatAlignCenter,
    label: t("outbox.compose.fmt_align_center"),
    active: () => !!editor.value?.isActive({ textAlign: "center" }),
    run: () => !!editor.value?.chain().focus().setTextAlign("center").run(),
  },
  {
    id: "right",
    icon: mdiFormatAlignRight,
    label: t("outbox.compose.fmt_align_right"),
    active: () => !!editor.value?.isActive({ textAlign: "right" }),
    run: () => !!editor.value?.chain().focus().setTextAlign("right").run(),
  },
]);

const sourceAction = actionOf("view");
const closeAction = actionOf("close");
</script>

<template>
  <div
    :class="{ 'rich-text-editor--disabled': disabled }"
    class="rich-text-editor"
    data-testid="rich-text-editor"
  >
    <div
      :aria-label="t('outbox.compose.toolbar')"
      class="rich-text-editor__toolbar d-flex flex-wrap align-center ga-1 pa-1"
      role="toolbar"
    >
      <template v-if="!sourceMode">
        <v-btn-tool
          v-for="btn in toolbar"
          :key="btn.id"
          :icon="btn.icon"
          :aria-label="btn.label"
          :aria-pressed="btn.active()"
          :color="btn.active() ? 'primary' : undefined"
          :disabled="disabled || !editor"
          @click="run(btn.run)"
        />
        <v-btn-tool
          :icon="mdiLinkVariant"
          :aria-label="t('outbox.compose.fmt_link')"
          :color="editor?.isActive('link') ? 'primary' : undefined"
          :disabled="disabled || !editor"
          @click="askLink"
        />
        <v-btn-tool
          :icon="mdiImageOutline"
          :aria-label="t('outbox.compose.fmt_image')"
          :disabled="disabled || !editor"
          @click="askImage"
        />
        <v-btn-tool
          :icon="mdiTable"
          :aria-label="t('outbox.compose.fmt_table')"
          :disabled="disabled || !editor"
          @click="insertTable"
        />
        <v-divider
          class="mx-1 align-self-stretch"
          vertical
        />
        <v-btn-tool
          :icon="mdiUndo"
          :aria-label="t('outbox.compose.fmt_undo')"
          :disabled="disabled || !editor?.can().undo()"
          @click="editor?.chain().focus().undo().run()"
        />
        <v-btn-tool
          :icon="mdiRedo"
          :aria-label="t('outbox.compose.fmt_redo')"
          :disabled="disabled || !editor?.can().redo()"
          @click="editor?.chain().focus().redo().run()"
        />
      </template>
      <v-spacer />
      <v-btn-tool
        :color="sourceMode ? closeAction.color : sourceAction.color"
        :prepend-icon="mdiCodeTags"
        :disabled="disabled"
        data-testid="rich-text-source-toggle"
        @click="sourceMode = !sourceMode"
      >
        {{ sourceMode ? t("outbox.compose.source_hide") : t("outbox.compose.source_show") }}
      </v-btn-tool>
    </div>

    <v-textarea
      v-if="sourceMode"
      v-model="sourceText"
      :disabled="disabled"
      :aria-label="t('outbox.compose.source_label')"
      class="rich-text-editor__source no-validation"
      data-testid="rich-text-source"
      rows="10"
      auto-grow
      hide-details
    />
    <EditorContent
      v-show="!sourceMode"
      :editor="editor"
      class="rich-text-editor__surface"
    />

    <div class="d-flex justify-end text-caption text-medium-emphasis mt-1 px-1">
      <span data-testid="rich-text-chars">
        {{
          maxLength > 0
            ? t("outbox.compose.chars_limited", { n: chars, max: maxLength })
            : t("outbox.compose.chars", { n: chars })
        }}
      </span>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.rich-text-editor {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  background: rgb(var(--v-theme-field));
  overflow: hidden;
  .v-field__outline {
    color: transparent;
  }
}

.rich-text-editor__toolbar {
  border-bottom: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface-variant));
}

.rich-text-editor__surface {
  min-height: 160px;
  max-height: 420px;
  overflow: auto;
}

.rich-text-editor__surface :deep(.tiptap),
.rich-text-editor__surface :deep(.ProseMirror) {
  min-height: 160px;
  padding: 12px 14px;
  outline: none;
  color: rgb(var(--v-theme-on-surface));
  font-size: 0.95rem;
  line-height: 1.5;

  p {
    margin: 0 0 0.6em;
  }

  ul,
  ol {
    padding-left: 1.4em;
    margin: 0 0 0.6em;
  }

  ul[data-type="taskList"] {
    list-style: none;
    padding-left: 0;

    li {
      display: flex;
      align-items: flex-start;
      gap: 0.5em;
    }
  }

  table {
    border-collapse: collapse;
    width: 100%;
    margin-bottom: 0.6em;

    td,
    th {
      border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
      padding: 4px 8px;
    }

    th {
      background: rgb(var(--v-theme-surface-variant));
    }
  }

  img {
    max-width: 100%;
    height: auto;
  }

  a {
    color: rgb(var(--v-theme-primary));
  }

  p.is-editor-empty:first-child::before {
    color: rgb(var(--v-theme-on-surface), 0.4);
    content: attr(data-placeholder);
    float: left;
    height: 0;
    pointer-events: none;
  }
}

.rich-text-editor__source {
  margin: 0;
}

.rich-text-editor--disabled {
  opacity: 0.65;
  pointer-events: none;
}
</style>
