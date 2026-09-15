import { expect, it } from 'vitest'

import router from './index'

it('keeps Gateway and 3x-ui routes without exposing an AimiliVPN settings page', () => {
  const routes = router.getRoutes()
  expect(routes.some(route => route.path === '/')).toBe(true)
  expect(routes.some(route => route.path === '/settings/3x-ui')).toBe(true)
  expect(routes.some(route => route.path === '/settings/aimilivpn')).toBe(false)
})
