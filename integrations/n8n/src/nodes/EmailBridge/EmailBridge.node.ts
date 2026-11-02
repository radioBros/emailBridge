import { EmailBridgeClient } from '../../client'

/** Minimal n8n node description: trigger (webhook subscribe) + send/list/search actions. */
export class EmailBridge {
  description = {
    displayName: 'EmailBridge',
    name: 'emailBridge',
    group: ['transform'],
    version: 1,
    description: 'Triggers on EmailBridge events; actions send / list / search messages',
    defaults: { name: 'EmailBridge' },
    inputs: ['main'],
    outputs: ['main'],
    credentials: [{ name: 'emailBridgeApi', required: true }],
    properties: [
      {
        displayName: 'Resource',
        name: 'resource',
        type: 'options',
        options: [
          { name: 'Trigger', value: 'trigger' },
          { name: 'Message', value: 'message' },
        ],
        default: 'message',
      },
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        options: [
          { name: 'Subscribe (REST hook)', value: 'subscribe' },
          { name: 'Send', value: 'send' },
          { name: 'List', value: 'list' },
          { name: 'Search', value: 'search' },
        ],
        default: 'send',
      },
      { displayName: 'Account ID', name: 'accountId', type: 'string', default: '' },
      { displayName: 'Webhook URL', name: 'webhookUrl', type: 'string', default: '' },
      { displayName: 'To', name: 'to', type: 'string', default: '' },
      { displayName: 'Subject', name: 'subject', type: 'string', default: '' },
      { displayName: 'Text', name: 'text', type: 'string', default: '' },
      { displayName: 'Query', name: 'q', type: 'string', default: '' },
    ],
  }

  async execute(this: {
    getCredentials: (name: string) => Promise<{ baseUrl: string; apiKey: string }>
    getNodeParameter: (name: string, i: number) => unknown
    helpers?: { httpRequest?: unknown }
    getInputData: () => Array<{ json: Record<string, unknown> }>
  }) {
    const creds = await this.getCredentials('emailBridgeApi')
    const client = new EmailBridgeClient({ baseUrl: creds.baseUrl, apiKey: creds.apiKey })
    const items = this.getInputData()
    const out: Array<{ json: Record<string, unknown> }> = []
    for (let i = 0; i < items.length; i++) {
      const operation = String(this.getNodeParameter('operation', i))
      const accountId = String(this.getNodeParameter('accountId', i) || '')
      if (operation === 'subscribe') {
        const url = String(this.getNodeParameter('webhookUrl', i))
        const ep = await client.createWebhookEndpoint({
          name: 'n8n EmailBridge trigger',
          url,
          events: ['*'],
        })
        out.push({ json: { endpointId: ep.id } })
      } else if (operation === 'send') {
        const to = String(this.getNodeParameter('to', i))
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
        const subject = String(this.getNodeParameter('subject', i))
        const text = String(this.getNodeParameter('text', i) || '')
        const res = await client.send(accountId, { to, subject, text })
        out.push({ json: { id: res.id } })
      } else if (operation === 'list') {
        const res = await client.listMessages(accountId)
        out.push({ json: { data: res.data } })
      } else if (operation === 'search') {
        const q = String(this.getNodeParameter('q', i))
        const res = await client.searchMessages(accountId, q)
        out.push({ json: { data: res.data } })
      }
    }
    return [out]
  }
}
