import { describe, expect, it, vi } from 'vitest'
import { listMessages, searchMessages, sendMessage, subscribeTrigger } from './client'

describe('activepieces client', () => {
  it('subscribes and runs actions against a mock API', async () => {
    const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      const u = String(url)
      const method = init?.method ?? 'GET'
      if (method === 'POST' && u.includes('webhook-endpoints')) {
        return new Response(JSON.stringify({ id: 'wh_ap' }), { status: 201 })
      }
      if (method === 'POST' && u.includes('/messages')) {
        return new Response(JSON.stringify({ id: 'msg_ap' }), { status: 201 })
      }
      if (u.includes('q=')) {
        return new Response(JSON.stringify({ data: [{ id: '1' }] }), { status: 200 })
      }
      return new Response(JSON.stringify({ data: [] }), { status: 200 })
    }) as typeof fetch

    const auth = { baseUrl: 'https://eb.test', apiKey: 'k' }
    const sub = (await subscribeTrigger(auth, 'https://hooks.ap/x', fetchMock)) as { id: string }
    expect(sub.id).toBe('wh_ap')
    const sent = (await sendMessage(
      auth,
      'acc',
      { to: ['a@b.c'], subject: 's' },
      fetchMock,
    )) as { id: string }
    expect(sent.id).toBe('msg_ap')
    await listMessages(auth, 'acc', fetchMock)
    const found = (await searchMessages(auth, 'acc', 's', fetchMock)) as { data: unknown[] }
    expect(found.data).toHaveLength(1)
  })
})
