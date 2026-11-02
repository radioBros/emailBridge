import { computed } from "vue";
import { useSessionStore } from "@/stores/session";
import {
  assignableRoles,
  canEditMember,
  canReadAudit,
  canWriteApiKeys,
  canWriteMembers,
} from "@/features/organization/helpers/roles";

/** Active organisation role from GET /v1/me. */
export const useOrgRole = () => {
  const session = useSessionStore();

  const role = computed(() => session.me?.role ?? null);

  return {
    role,
    canWriteMembers: computed(() => canWriteMembers(role.value)),
    canWriteApiKeys: computed(() => canWriteApiKeys(role.value)),
    canReadAudit: computed(() => canReadAudit(role.value)),
    assignableRoles: computed(() => assignableRoles(role.value)),
    canEditMember: (targetRole: string) => canEditMember(role.value, targetRole),
  };
};
