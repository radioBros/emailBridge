import { describe, expect, it, vi } from 'vitest'
import { EmailBridgeClient } from './client.js'

describe('Zapier EmailBridgeClient', () => {
  it('creates a webhook subscription via mock request', async () => {
    const request = vi.fn(async (opts) => {
      expect(opts.method).toBe('POST')
      expect(opts.url).toContain('/v1/webhook-endpoints')
      return { id: 'wh_z' }
    })
    const c = new EmailBridgeClient({ baseUrl: 'https://eb.test/', apiKey: 'k' }, request)
    const ep = await c.createWebhookEndpoint({ name: 'zap', url: 'https://hooks.zapier.com/x' })
    expect(ep.id).toBe('wh_z')
    const sentReq = vi.fn(async () => ({ id: 'msg_z' }))
    const c2 = new EmailBridgeClient({ baseUrl: 'https://eb.test', apiKey: 'k' }, sentReq)
    const msg = await c2.send('acc', { to: ['a@b.c'], subject: 's' })
    expect(msg.id).toBe('msg_z')
  })
})
