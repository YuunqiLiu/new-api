/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { getUserQuotaDates } from '@/features/dashboard/api'
import { LogStatCards } from '@/features/dashboard/components/models/log-stat-cards'

vi.mock('@/features/dashboard/api', () => ({
  getUserQuotaDates: vi.fn(),
}))

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.clear()
})

describe('model usage request failures', () => {
  it.each([
    ['business failure', { success: false, data: [] }],
    ['malformed successful response', { success: true, data: {} }],
  ])('does not report zero consumption for %s', async (_, response) => {
    vi.mocked(getUserQuotaDates).mockResolvedValue(
      response as Awaited<ReturnType<typeof getUserQuotaDates>>
    )
    const onDataUpdate = vi.fn()
    render(<LogStatCards onDataUpdate={onDataUpdate} />)

    expect(await screen.findByRole('alert')).toHaveTextContent('Request failed')
    expect(screen.getAllByText('--')).toHaveLength(5)
    expect(onDataUpdate).toHaveBeenLastCalledWith([], false, true)
  })

  it('reports failure to the parent when the request rejects', async () => {
    vi.mocked(getUserQuotaDates).mockRejectedValue(
      new Error('network unavailable')
    )
    const onDataUpdate = vi.fn()
    render(<LogStatCards onDataUpdate={onDataUpdate} />)

    expect(await screen.findByRole('alert')).toHaveTextContent('Request failed')
    expect(screen.getAllByText('--')).toHaveLength(5)
    expect(onDataUpdate).toHaveBeenLastCalledWith([], false, true)
  })

  it.each([
    { name: 'empty array', data: [] },
    { name: 'null Go slice', data: null },
  ])(
    'reports a successful $name as zero usage without an error',
    async ({ data }) => {
      vi.mocked(getUserQuotaDates).mockResolvedValue({
        success: true,
        data,
      } as Awaited<ReturnType<typeof getUserQuotaDates>>)
      const onDataUpdate = vi.fn()
      render(<LogStatCards onDataUpdate={onDataUpdate} />)

      await waitFor(() =>
        expect(onDataUpdate).toHaveBeenLastCalledWith([], false, false)
      )
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(screen.queryByText('--')).not.toBeInTheDocument()
      expect(screen.getAllByText('0').length).toBeGreaterThan(0)
    }
  )
})
