import { describe, expect, it, vi } from 'vitest'
import { EmailBridgeClient } from './client'

describe('EmailBridgeClient', () => {
  it('subscribes a webhook and sends a message against a mock API', async () => {
    const calls: Array<{ url: string; method: string; body?: string }> = []
    const fetchMock: typeof fetch = vi.fn(async (input, init) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = init?.body ? String(init.body) : undefined
      calls.push({ url, method, body })
      if (method === 'POST' && url.endsWith('/v1/webhook-endpoints')) {
        return new Response(JSON.stringify({ id: 'wh_1' }), { status: 201 })
      }
      if (method === 'POST' && url.includes('/messages')) {
        return new Response(JSON.stringify({ id: 'msg_1' }), { status: 201 })
      }
      if (method === 'GET' && url.includes('q=')) {
        return new Response(JSON.stringify({ data: [{ id: 'msg_2' }] }), { status: 200 })
      }
      return new Response(JSON.stringify({ data: [] }), { status: 200 })
    }) as typeof fetch

    const c = new EmailBridgeClient({
      baseUrl: 'https://eb.test',
      apiKey: 'key',
      fetch: fetchMock,
    })
    const ep = await c.createWebhookEndpoint({ name: 'n8n', url: 'https://hook', events: ['*'] })
    expect(ep.id).toBe('wh_1')
    const sent = await c.send('acc_1', { to: ['a@b.c'], subject: 'hi', text: 'body' })
    expect(sent.id).toBe('msg_1')
    const found = await c.searchMessages('acc_1', 'hi')
    expect(found.data).toHaveLength(1)
    expect(calls.some((c) => c.method === 'POST' && c.url.includes('webhook-endpoints'))).toBe(true)
  })
})
