const { EmailBridgeClient } = require('./lib/client')

const authentication = {
  type: 'custom',
  fields: [
    { key: 'baseUrl', label: 'Base URL', required: true },
    { key: 'apiKey', label: 'API Key', required: true, type: 'password' },
  ],
  test: async (z, bundle) => {
    const c = new EmailBridgeClient(bundle.authData, z.request.bind(z))
    return c.request('GET', '/v1/integrations')
  },
}

const eventTrigger = {
  key: 'event',
  noun: 'Event',
  display: { label: 'New Event', description: 'Triggers when EmailBridge emits a matching event.' },
  operation: {
    type: 'hook',
    performSubscribe: async (z, bundle) => {
      const c = new EmailBridgeClient(bundle.authData, z.request.bind(z))
      const ep = await c.createWebhookEndpoint({
        name: 'Zapier EmailBridge',
        url: bundle.targetUrl,
        events: bundle.inputData.events || ['*'],
      })
      return { id: ep.id }
    },
    performUnsubscribe: async (z, bundle) => {
      const c = new EmailBridgeClient(bundle.authData, z.request.bind(z))
      await c.deleteWebhookEndpoint(bundle.subscribeData.id)
      return {}
    },
    perform: async (z, bundle) => [bundle.cleanedRequest],
    inputFields: [{ key: 'events', label: 'Event types (CSV or *)', default: '*' }],
    sample: { id: 'evt_sample', type: 'message.received' },
  },
}

const sendMessage = {
  key: 'send_message',
  noun: 'Message',
  display: { label: 'Send Message', description: 'Send mail through an EmailBridge account.' },
  operation: {
    perform: async (z, bundle) => {
      const c = new EmailBridgeClient(bundle.authData, z.request.bind(z))
      return c.send(bundle.inputData.accountId, {
        to: String(bundle.inputData.to || '')
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        subject: bundle.inputData.subject,
        text: bundle.inputData.text,
      })
    },
    inputFields: [
      { key: 'accountId', required: true },
      { key: 'to', required: true },
      { key: 'subject', required: true },
      { key: 'text' },
    ],
    sample: { id: 'msg_sample' },
  },
}

const listMessages = {
  key: 'list_messages',
  noun: 'Message',
  display: { label: 'List Messages', description: 'List messages for an account.' },
  operation: {
    perform: async (z, bundle) => {
      const c = new EmailBridgeClient(bundle.authData, z.request.bind(z))
      const res = await c.listMessages(bundle.inputData.accountId)
      return res.data || []
    },
    inputFields: [{ key: 'accountId', required: true }],
    sample: { id: 'msg_sample' },
  },
}

const searchMessages = {
  key: 'search_messages',
  noun: 'Message',
  display: { label: 'Search Messages', description: 'Search messages for an account.' },
  operation: {
    perform: async (z, bundle) => {
      const c = new EmailBridgeClient(bundle.authData, z.request.bind(z))
      const res = await c.searchMessages(bundle.inputData.accountId, bundle.inputData.q || '')
      return res.data || []
    },
    inputFields: [
      { key: 'accountId', required: true },
      { key: 'q', required: true },
    ],
    sample: { id: 'msg_sample' },
  },
}

module.exports = {
  version: require('./package.json').version,
  platformVersion: '15.0.0',
  authentication,
  triggers: { [eventTrigger.key]: eventTrigger },
  creates: {
    [sendMessage.key]: sendMessage,
    [listMessages.key]: listMessages,
    [searchMessages.key]: searchMessages,
  },
}
