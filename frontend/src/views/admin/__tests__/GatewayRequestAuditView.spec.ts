import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import GatewayRequestAuditView from '../GatewayRequestAuditView.vue'

const audit = vi.hoisted(() => ({
  state: vi.fn(),
  list: vi.fn(),
  get: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { gatewayRequestAudit: audit } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' }
}))
vi.mock('@/components/common/BaseDialog.vue', () => ({
  default: { props: ['show'], template: '<section v-if="show"><slot /></section>' }
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

it('fetches one page of summaries and loads a full record only when its details are opened', async () => {
  const summary = {
    id: 'older-record',
    transport: 'http',
    captured_at: '2026-11-25T00:00:00Z',
    downstream_method: 'POST',
    downstream_url: '/v1/responses',
    upstream_method: 'POST',
    upstream_url: 'https://example.com/responses'
  }
  audit.state.mockResolvedValue({ active: false, session_id: 'session', count: 21, bytes: 100 })
  audit.list
    .mockResolvedValueOnce({
      items: [{ ...summary, id: 'newest-record', downstream_url: '/v1/newest' }],
      page: 1,
      page_size: 20,
      pages: 2,
      total: 21
    })
    .mockResolvedValueOnce({ items: [summary], page: 2, page_size: 20, pages: 2, total: 21 })
  audit.get.mockResolvedValue({
    ...summary,
    session_id: 'session',
    downstream_headers: { 'X-Test': ['downstream-header'] },
    upstream_headers: {},
    downstream_body: 'full request body'
  })

  const wrapper = mount(GatewayRequestAuditView)
  try {
    await flushPromises()
    expect(audit.list).toHaveBeenCalledWith(1, 20, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('/v1/newest')
    expect(audit.get).not.toHaveBeenCalled()

    await wrapper.get('nav button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    expect(audit.list).toHaveBeenLastCalledWith(2, 20, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('/v1/responses')
    expect(wrapper.text()).not.toContain('/v1/newest')
    expect(audit.get).not.toHaveBeenCalled()

    const detail = wrapper.get('tbody button')
    await detail.trigger('click')
    await flushPromises()
    expect(audit.get).toHaveBeenCalledTimes(1)
    expect(audit.get).toHaveBeenCalledWith('older-record', expect.any(AbortSignal))
    expect(wrapper.text()).toContain('downstream-header')
    expect(wrapper.text()).toContain('full request body')
  } finally {
    wrapper.unmount()
  }
})
