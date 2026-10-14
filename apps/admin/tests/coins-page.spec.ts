// @vitest-environment jsdom
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as Vue from 'vue'

import { mountPage, sessionRef, settle, stubNuxtGlobals } from './helpers/nuxtStubs'
import Coins from '../src/pages/config/coins.vue'

// The Coin editor (issue #61): the wallet's Coin config list with an
// inline editor for super-admin. Only changed fields travel — the
// wallet treats absent fields as keep, and an untouched xpub field
// must never wipe the stored custody key.

const collaborators = vi.hoisted(() => ({
  apiFetch: vi.fn(),
}))

const btc = {
  name: 'BTC', family: 'btc', native: true, contract: '',
  chainDecimals: 8, scale: 8, confirmations: 3,
  withdrawFee: '0.0005', minWithdraw: '0.001', withdrawable: true,
  sweepThreshold: '0.05', sweepMinSeconds: 3600, sweepPaused: false,
  coldAddress: '', hotCeiling: '0', hotFloor: '0',
}

describe('coins page', () => {
  const session = sessionRef(null)

  beforeEach(() => {
    collaborators.apiFetch.mockReset()
    collaborators.apiFetch.mockImplementation(async (path: string) => {
      if (path === '/api/coins') {
        return [{ ...btc }]
      }
      return {}
    })
    stubNuxtGlobals(Vue, {
      useApi: () => ({ apiFetch: collaborators.apiFetch }),
      useAdminSession: () => ({ session }),
    })
  })

  async function mountCoins(): Promise<VueWrapper> {
    const wrapper = mountPage(Coins)
    await flushPromises()
    await settle()
    return wrapper
  }

  function editButton(wrapper: VueWrapper) {
    return wrapper.findAll('button').find((b) => b.text() === 'Edit')
  }

  async function openEditor(wrapper: VueWrapper): Promise<void> {
    await editButton(wrapper)!.trigger('click')
    await settle()
  }

  it('lists the Coin config and opens the editor for a permitted Employee', async () => {
    session.value = {
      employee: { id: 'e-1', email: 'root@x.test', role: 'super-admin' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:coin', 'edit:coin'],
    }
    const wrapper = await mountCoins()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/coins')
    expect(wrapper.text()).toContain('BTC')
    expect(wrapper.text()).toContain('0.0005')
    expect(editButton(wrapper)).toBeTruthy()
  })

  it('sends only the changed fields and never an empty xpub', async () => {
    session.value = {
      employee: { id: 'e-1', email: 'root@x.test', role: 'super-admin' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:coin', 'edit:coin'],
    }
    const wrapper = await mountCoins()
    await openEditor(wrapper)

    await wrapper.find('input[name="confirmations"]').setValue('6')
    await wrapper.find('input[name="sweepPaused"]')?.setValue(true)
    await wrapper.find('input[name="xpub"]').setValue('xpub661MyMw')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(collaborators.apiFetch).toHaveBeenCalledWith('/api/coins/BTC', {
      method: 'PUT',
      body: {
        confirmations: 6,
        sweepPaused: true,
        xpub: 'xpub661MyMw',
      },
    })
    // Initial list + the PUT + the post-save re-list.
    expect(collaborators.apiFetch).toHaveBeenCalledTimes(3)
  })

  it('renders the api sentence when an edit is refused', async () => {
    session.value = {
      employee: { id: 'e-1', email: 'root@x.test', role: 'super-admin' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:coin', 'edit:coin'],
    }
    collaborators.apiFetch.mockImplementation(async (path: string) => {
      if (path === '/api/coins') {
        return [{ ...btc }]
      }
      throw new Error('scale must be between 0 and 18')
    })
    const wrapper = await mountCoins()
    await openEditor(wrapper)

    await wrapper.find('input[name="scale"]').setValue('99')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await settle()

    expect(wrapper.find('.el-alert__title').text()).toContain('scale must be between 0 and 18')
    expect(wrapper.find('.editor').exists()).toBe(true)
  })

  it('is read-only without the edit permission', async () => {
    session.value = {
      employee: { id: 'e-2', email: 'view@x.test', role: 'finance' },
      mustRotate: false,
      totpBound: true,
      permissions: ['view:coin'],
    }
    const wrapper = await mountCoins()

    expect(wrapper.text()).toContain('BTC')
    expect(editButton(wrapper)).toBeUndefined()
  })
})
