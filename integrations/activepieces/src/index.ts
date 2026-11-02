import {
  listMessages,
  searchMessages,
  sendMessage,
  subscribeTrigger,
} from './client'

/** Activepieces piece metadata (simplified; wire into @activepieces/pieces-framework at publish). */
export const emailBridgePiece = {
  name: 'emailbridge',
  displayName: 'EmailBridge',
  description: 'Triggers on mail events; actions send / list / search',
  auth: {
    type: 'CUSTOM_AUTH',
    props: {
      baseUrl: { displayName: 'Base URL', required: true },
      apiKey: { displayName: 'API Key', required: true },
    },
  },
  triggers: {
    newEvent: {
      name: 'new_event',
      displayName: 'New Event',
      type: 'WEBHOOK',
      onEnable: subscribeTrigger,
    },
  },
  actions: {
    send: { name: 'send_message', displayName: 'Send Message', run: sendMessage },
    list: { name: 'list_messages', displayName: 'List Messages', run: listMessages },
    search: { name: 'search_messages', displayName: 'Search Messages', run: searchMessages },
  },
}

export * from './client'
