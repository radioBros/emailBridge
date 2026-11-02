/** @type {import('stylelint').Config} */
export default {
  extends: [
    "stylelint-config-standard-scss",
    "stylelint-config-recommended-vue/scss",
  ],
  rules: {
    "selector-class-pattern": null,
    "custom-property-pattern": null,
    "scss/load-no-partial-leading-underscore": null,
    "scss/comment-no-empty": null,
    "no-descending-specificity": null,
    "color-function-notation": null,
    "alpha-value-notation": null,
    "no-empty-source": null,
  },
};
