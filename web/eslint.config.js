import js from "@eslint/js";
import pluginVue from "eslint-plugin-vue";
import tseslint from "typescript-eslint";
import vueParser from "vue-eslint-parser";
import globals from "globals";

export default tseslint.config(
  {
    ignores: [
      "dist/**",
      "coverage/**",
      "node_modules/**",
      "build/**",
      "scripts/**",
      "playwright-report/**",
      "e2e/screens/**",
    ],
  },
  {
    files: ["e2e/**/*.{ts,js}", "playwright.config.ts"],
    languageOptions: {
      globals: {
        ...globals.node,
      },
    },
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs["flat/recommended"],
  {
    files: ["**/*.{ts,vue}"],
    languageOptions: {
      parser: vueParser,
      parserOptions: {
        parser: tseslint.parser,
        ecmaVersion: "latest",
        sourceType: "module",
        extraFileExtensions: [".vue"],
      },
      globals: {
        ...globals.browser,
        ...globals.es2022,
        __APP_VERSION__: "readonly",
        __GIT_SHA__: "readonly",
      },
    },
    rules: {
      "vue/multi-word-component-names": "off",
      "vue/valid-template-root": "off",
      "vue/html-self-closing": "off",
      "vue/max-attributes-per-line": [
        "error",
        {
          singleline: { max: 4 },
          multiline: { max: 1 },
        },
      ],
      "vue/attributes-order": [
        "error",
        {
          order: [
            "DEFINITION",
            "LIST_RENDERING",
            "CONDITIONALS",
            "RENDER_MODIFIERS",
            "GLOBAL",
            ["UNIQUE", "SLOT"],
            "TWO_WAY_BINDING",
            "OTHER_DIRECTIVES",
            "ATTR_DYNAMIC",
            "ATTR_STATIC",
            "ATTR_SHORTHAND_BOOL",
            "EVENTS",
            "CONTENT",
          ],
          alphabetical: false,
        },
      ],
      "vue/no-restricted-v-bind": [
        "error",
        {
          argument: "/^(rounded|variant|density)$/",
          message:
            "Do not bind rounded/variant/density — set them in VuetifyDefaults or use a static attribute.",
        },
      ],
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "@typescript-eslint/no-explicit-any": "off",
    },
  },
);
