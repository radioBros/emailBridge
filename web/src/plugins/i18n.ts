import created from "@/app/i18n";

/** Loosely typed re-export so consumers like Validations.ts do not explode vue-tsc. */
export default created as {
  global: {
    t: (key: string, named?: Record<string, unknown>) => string;
    te: (key: string) => boolean;
    locale: { value: string };
  };
};
