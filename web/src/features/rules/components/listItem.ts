/** Shape of the option objects the rules pickers pass to AutocompleteSearch's #item slot. */
export interface ListOption {
  title: string;
  subtitle?: string;
  [key: string]: unknown;
}
