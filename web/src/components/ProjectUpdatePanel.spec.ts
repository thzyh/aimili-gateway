import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const { fetchAPI } = vi.hoisted(() => ({ fetchAPI: vi.fn() }))
vi.mock('../api/client', () => ({ apiFetch: fetchAPI, APIError: class APIError extends Error { constructor(public status: number, code: string) { super(code) } } }))
import ProjectUpdatePanel from './ProjectUpdatePanel.vue'
const initial = { enabled: true, project: true, currentGateway: 'legacy-eea92cb', available: [] }
let action = 'check'
beforeEach(() => {
  vi.useFakeTimers(); localStorage.clear(); fetchAPI.mockReset(); action = 'check'
  fetchAPI.mockImplementation(async (path: string, options?: RequestInit) => {
    if (options?.method === 'POST') { action = path.endsWith('/apply') ? 'apply' : 'check'; return {} }
    if (path === '/api/v1/system/updates') return { ...initial, available: [{ kind: 'project', version: 'v0.2.12-vps', compatible: true }] }
    return { runId: 'a'.repeat(64), kind: 'project', action, state: action === 'check' ? 'success' : 'downloading', phase: 'download', percent: 23 }
  })
})
afterEach(() => vi.useRealTimers())

it('discovers signed server catalog and cancel never posts an apply', async () => {
  const view = mount(ProjectUpdatePanel, { props: { initial } })
  await view.get('[data-check-project]').trigger('click'); await flushPromises()
  expect(view.get('[role="alertdialog"]').text()).toContain('v0.2.12-vps')
  expect(fetchAPI.mock.calls.filter(x => x[1]?.method === 'POST')).toHaveLength(1)
  await view.get('[data-cancel-project]').trigger('click')
  expect(view.find('[role="alertdialog"]').exists()).toBe(false)
  expect(fetchAPI.mock.calls.some(x => x[0].endsWith('/apply'))).toBe(false)
  view.unmount()
})

it('shows the specific blocker and safe upgrade route without offering apply', async () => {
  fetchAPI.mockImplementation(async (path: string, options?: RequestInit) => {
    if (options?.method === 'POST') return {}
    if (path === '/api/v1/system/updates') return { ...initial, available: [{ kind: 'project', version: 'v0.2.14-vps', compatible: false, reasonCode: 'unsupported_component', component: 'caddy', upgradePath: 'https://github.com/thzyh/aimili-gateway/blob/main/docs/upgrade.md#unsupported-component' }] }
    return { runId: 'a'.repeat(64), kind: 'project', action: 'check', state: 'success' }
  })
  const view = mount(ProjectUpdatePanel, { props: { initial } })
  await view.get('[data-check-project]').trigger('click'); await flushPromises()
  expect(view.text()).toContain('Caddy')
  expect(view.get('[data-update-guide]').attributes('href')).toContain('#unsupported-component')
  expect(view.get('[data-update-remedy]').text()).toContain('过渡版本')
  expect(view.find('[data-confirm-project]').exists()).toBe(false)
  expect(fetchAPI.mock.calls.some(x => x[0].endsWith('/apply'))).toBe(false)
  view.unmount()
})

it('only confirms one pinned apply and shows actual progress', async () => {
  const view = mount(ProjectUpdatePanel, { props: { initial } })
  await view.get('[data-check-project]').trigger('click'); await flushPromises()
  await view.get('[data-confirm-project]').trigger('click'); await flushPromises()
  expect(fetchAPI.mock.calls.filter(x => x[0].endsWith('/apply'))).toHaveLength(1)
  expect(fetchAPI).toHaveBeenCalledWith('/api/v1/system/updates/project/v0.2.12-vps/apply', expect.objectContaining({ method: 'POST' }))
  expect(view.get('progress').attributes('value')).toBe('23')
  view.unmount()
})

it('resumes the server transaction after page refresh without a second POST', async () => {
  action = 'apply'
  const view = mount(ProjectUpdatePanel, { props: { initial: { ...initial, activeRunId: 'b'.repeat(64) } } })
  await flushPromises()
  expect(fetchAPI).toHaveBeenCalledWith(`/api/v1/system/updates/${'b'.repeat(64)}`)
  expect(fetchAPI.mock.calls.some(x => x[1]?.method === 'POST')).toBe(false)
  expect(view.text()).toContain('23%')
  view.unmount()
})

it('reconnects during restart and displays rollback instead of success', async () => {
  fetchAPI.mockRejectedValueOnce(new Error('restart')).mockResolvedValue({ runId: 'b'.repeat(64), kind: 'project', action: 'apply', state: 'rolled_back' })
  const view = mount(ProjectUpdatePanel, { props: { initial: { ...initial, activeRunId: 'b'.repeat(64) } } })
  await flushPromises()
  expect(view.text()).toContain('正在重新连接')
  await vi.advanceTimersByTimeAsync(2000); await flushPromises()
  expect(view.text()).toContain('已自动恢复升级前版本和数据库')
  expect(fetchAPI.mock.calls.some(x => x[1]?.method === 'POST')).toBe(false)
  view.unmount()
})
