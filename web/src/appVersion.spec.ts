import { beforeEach, expect, it, vi } from 'vitest'

import { checkForAppUpdate, entryScriptFromHTML } from './appVersion'

const baseUrl = 'https://gateway.example.test/'

beforeEach(() => sessionStorage.clear())

it('extracts and normalizes the deployed module entry', () => {
  expect(entryScriptFromHTML('<script type="module" src="/assets/index-new.js"></script>', baseUrl))
    .toBe('https://gateway.example.test/assets/index-new.js')
})

it('reloads once when the deployed entry changes', async () => {
  const reload = vi.fn()
  const fetchImpl = vi.fn().mockResolvedValue(new Response(
    '<script type="module" src="/assets/index-new.js"></script>',
    { status: 200, headers: { 'Content-Type': 'text/html' } },
  ))

  await expect(checkForAppUpdate({
    currentEntry: '/assets/index-old.js', fetchImpl, reload, storage: sessionStorage, baseUrl,
  })).resolves.toBe(true)
  expect(reload).toHaveBeenCalledTimes(1)

  await expect(checkForAppUpdate({
    currentEntry: '/assets/index-old.js', fetchImpl, reload, storage: sessionStorage, baseUrl,
  })).resolves.toBe(false)
  expect(reload).toHaveBeenCalledTimes(1)
})

it('does not reload for the current entry or a failed version check', async () => {
  const reload = vi.fn()
  const current = vi.fn().mockResolvedValue(new Response(
    '<script type="module" src="/assets/index-current.js"></script>',
    { status: 200 },
  ))
  const failed = vi.fn().mockRejectedValue(new Error('offline'))

  await expect(checkForAppUpdate({
    currentEntry: '/assets/index-current.js', fetchImpl: current, reload, storage: sessionStorage, baseUrl,
  })).resolves.toBe(false)
  await expect(checkForAppUpdate({
    currentEntry: '/assets/index-current.js', fetchImpl: failed, reload, storage: sessionStorage, baseUrl,
  })).resolves.toBe(false)
  expect(reload).not.toHaveBeenCalled()
})
