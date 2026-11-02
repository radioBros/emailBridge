import {
  mdiAccountPlus,
  mdiCancel,
  mdiCheck,
  mdiClose,
  mdiCog,
  mdiContentCopy,
  mdiContentSave,
  mdiDelete,
  mdiDownload,
  mdiEye,
  mdiFilterVariant,
  mdiFlask,
  mdiIdentifier,
  mdiKeyChange,
  mdiKeyRemove,
  mdiLanConnect,
  mdiOpenInNew,
  mdiPauseCircle,
  mdiPencil,
  mdiPlayCircle,
  mdiPlus,
  mdiRadar,
  mdiRefresh,
  mdiReplay,
  mdiSend,
  mdiSendCheck,
  mdiShieldCheck,
  mdiSync,
  mdiUpload,
} from "@mdi/js";

export type ActionColor =
  | "success"
  | "primary"
  | "info"
  | "secondary"
  | "warning"
  | "error";

export interface ActionSpec {
  color: ActionColor;
  icon: string;
}

/**
 * One map for action → colour + icon (S13 §2.2 + W78). Never hard-code colours on
 * buttons — always `actionOf(...)`. Cancel/close stay secondary (tonal, not flat grey).
 */
export const Actions = {
  // create / add / save / confirm / send
  create: { color: "success", icon: mdiPlus },
  add: { color: "success", icon: mdiPlus },
  invite: { color: "success", icon: mdiAccountPlus },
  save: { color: "success", icon: mdiContentSave },
  apply: { color: "success", icon: mdiCheck },
  confirm: { color: "success", icon: mdiCheck },
  enable: { color: "success", icon: mdiPlayCircle },
  resume: { color: "success", icon: mdiPlayCircle },
  send: { color: "success", icon: mdiSend },

  // edit / open / navigate / filter / settings
  edit: { color: "primary", icon: mdiPencil },
  view: { color: "primary", icon: mdiEye },
  open: { color: "primary", icon: mdiOpenInNew },
  navigate: { color: "primary", icon: mdiOpenInNew },
  filter: { color: "primary", icon: mdiFilterVariant },
  settings: { color: "primary", icon: mdiCog },
  configure: { color: "primary", icon: mdiCog },

  // test / export / import / live / ids / copy
  test: { color: "info", icon: mdiFlask },
  verify: { color: "info", icon: mdiShieldCheck },
  sendTest: { color: "info", icon: mdiSendCheck },
  export: { color: "info", icon: mdiDownload },
  download: { color: "info", icon: mdiDownload },
  import: { color: "info", icon: mdiUpload },
  upload: { color: "info", icon: mdiUpload },
  live: { color: "info", icon: mdiRadar },
  stream: { color: "info", icon: mdiRadar },
  ids: { color: "info", icon: mdiIdentifier },
  copy: { color: "info", icon: mdiContentCopy },

  // refresh / retry / sync — secondary (teal), not grey
  sync: { color: "secondary", icon: mdiSync },
  reconnect: { color: "secondary", icon: mdiLanConnect },
  refresh: { color: "secondary", icon: mdiRefresh },
  retry: { color: "secondary", icon: mdiReplay },
  replay: { color: "secondary", icon: mdiReplay },

  // cancel / close — neutral but tonal-tinted
  cancel: { color: "secondary", icon: mdiCancel },
  close: { color: "secondary", icon: mdiClose },

  // caution
  rotateSecret: { color: "warning", icon: mdiKeyChange },
  disable: { color: "warning", icon: mdiPauseCircle },
  pause: { color: "warning", icon: mdiPauseCircle },
  /** Live tail / stream while actively connected. */
  liveActive: { color: "warning", icon: mdiRadar },

  // destructive
  delete: { color: "error", icon: mdiDelete },
  revoke: { color: "error", icon: mdiKeyRemove },
  remove: { color: "error", icon: mdiDelete },
} as const satisfies Record<string, ActionSpec>;

export type ActionKey = keyof typeof Actions;

export const actionOf = (key: ActionKey): ActionSpec => Actions[key];
