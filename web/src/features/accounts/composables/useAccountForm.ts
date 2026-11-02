import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  createAccount,
  getAccount,
  patchAccount,
  verifyAccountSettings,
  type AccountView,
  type VerifyResult,
} from "@/api/accounts";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import {
  emptyForm,
  formFromAccount,
  toCreateInput,
  toPatchBody,
  toVerifyRequest,
  type AccountFormModel,
  type AccountViewG4,
} from "@/features/accounts/helpers/accountForm";
import {
  metadataFieldErrors,
  validateMetadataRows,
} from "@/features/accounts/helpers/accountMetadata";

export const FORM_STEPS = ["identity", "imap", "smtp", "sync", "verify"] as const;
export type FormStep = (typeof FORM_STEPS)[number];

/** Add / edit account stepper: load, verify, save. */
export const useAccountForm = () => {
  const route = useRoute();
  const router = useRouter();
  const { t } = useI18n();
  const notify = useNotifyStore();
  const { messageOf, fieldErrors } = useProblem();
  const { isFirstLoad, isRefreshing, start, finish, reset } = useLoadState();

  const accountId = computed(() =>
    typeof route.params.id === "string" ? route.params.id : undefined,
  );
  const isEdit = computed(() => Boolean(accountId.value));

  const form = ref<AccountFormModel>(emptyForm());
  const account = ref<AccountView | null>(null);
  const step = ref(1);
  const loadError = ref<string | null>(null);
  const saving = ref(false);
  const verifying = ref(false);
  const verifyResult = ref<VerifyResult | null>(null);
  const verifyError = ref<string | null>(null);
  const hasPassword = ref({ imap: false, smtp: false });
  /** Server field errors of the last save, by metadata key (G4). */
  const metadataErrors = ref<Record<string, string>>({});
  const metadataValid = computed(() => validateMetadataRows(form.value.metadata).valid);

  const stepCount = FORM_STEPS.length;

  const titleKey = computed(() =>
    isEdit.value ? "accounts.edit_title" : "accounts.create_title",
  );

  const load = async (): Promise<void> => {
    const id = accountId.value;
    if (!id) {
      form.value = emptyForm();
      account.value = null;
      hasPassword.value = { imap: false, smtp: false };
      finish(true);
      return;
    }
    start();
    loadError.value = null;
    try {
      const a = await getAccount(id);
      account.value = a;
      form.value = formFromAccount(a);
      hasPassword.value = {
        imap: a.imap.auth.hasPassword,
        smtp: a.smtp.auth.hasPassword,
      };
      finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const goNext = (): void => {
    if (step.value < stepCount) step.value += 1;
  };

  const goPrev = (): void => {
    if (step.value > 1) step.value -= 1;
  };

  const runVerify = async (): Promise<boolean> => {
    verifying.value = true;
    verifyError.value = null;
    verifyResult.value = null;
    try {
      verifyResult.value = await verifyAccountSettings(
        toVerifyRequest(form.value, isEdit.value),
      );
      return Boolean(verifyResult.value.imap?.ok && verifyResult.value.smtp?.ok);
    } catch (err) {
      verifyError.value = messageOf(problemOf(err) ?? err);
      return false;
    } finally {
      verifying.value = false;
    }
  };

  /** True when the last verify run succeeded for both protocols. */
  const verifyPassed = computed(
    () => Boolean(verifyResult.value?.imap?.ok && verifyResult.value?.smtp?.ok),
  );

  /**
   * Persist the form. Pass `skipVerify: true` only after the user confirmed
   * saving without a successful verification (create). Never set it silently.
   */
  const save = async (opts?: { skipVerify?: boolean }): Promise<boolean> => {
    if (!metadataValid.value) {
      step.value = 1;
      return false;
    }
    saving.value = true;
    metadataErrors.value = {};
    try {
      if (isEdit.value && account.value) {
        const updated = await patchAccount(
          account.value.id,
          toPatchBody(form.value, (account.value as AccountViewG4).metadata),
          account.value.version,
        );
        notify.push({ message: t("accounts.toast_saved"), color: "success" });
        await router.push(`/accounts/${updated.id}`);
      } else {
        const body = toCreateInput(form.value);
        if (opts?.skipVerify) body.skipVerify = true;
        const created = await createAccount(body);
        notify.push({ message: t("accounts.toast_created"), color: "success" });
        await router.push(`/accounts/${created.id}`);
      }
      return true;
    } catch (err) {
      metadataErrors.value = metadataFieldErrors(fieldErrors(problemOf(err)));
      if (Object.keys(metadataErrors.value).length) step.value = 1;
      notify.push({
        message: messageOf(problemOf(err) ?? err),
        color: "error",
        code: problemOf(err)?.code,
        traceId: problemOf(err)?.traceId,
      });
      return false;
    } finally {
      saving.value = false;
    }
  };

  const reload = async (): Promise<void> => {
    reset();
    await load();
  };

  return {
    accountId,
    isEdit,
    form,
    account,
    step,
    stepCount,
    titleKey,
    loadError,
    saving,
    verifying,
    verifyResult,
    verifyError,
    verifyPassed,
    hasPassword,
    metadataErrors,
    metadataValid,
    isFirstLoad,
    isRefreshing,
    load,
    reload,
    goNext,
    goPrev,
    runVerify,
    save,
  };
};
