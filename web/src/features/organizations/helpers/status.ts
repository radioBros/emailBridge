export const orgStatusColor = (status: string): string => {
  switch (status) {
    case "active":
      return "success";
    case "suspended":
      return "warning";
    case "deleting":
      return "error";
    default:
      return "default";
  }
};
