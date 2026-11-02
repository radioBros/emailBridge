/**
 * Template typing for the W67 role aliases registered in `componentAliases`
 * (src/core/config/VuetifyDefaults.ts): each alias takes the base component's props.
 */
import type { VAvatar, VBtn, VIcon } from "vuetify/components";

declare module "vue" {
  export interface GlobalComponents {
    VBtnInline: typeof VBtn;
    VBtnAffix: typeof VBtn;
    VBtnFieldIcon: typeof VBtn;
    VAvatarHero: typeof VAvatar;
    VAvatarLead: typeof VAvatar;
    VAvatarSmall: typeof VAvatar;
    VIconInline: typeof VIcon;
    VIconStatus: typeof VIcon;
    VIconHero: typeof VIcon;
  }
}

export {};
