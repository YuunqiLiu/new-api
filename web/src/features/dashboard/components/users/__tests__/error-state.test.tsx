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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ThemeProvider } from '@/context/theme-provider'
import { getUserQuotaDataByUsers } from '@/features/dashboard/api'
import { UserCharts } from '@/features/dashboard/components/users/user-charts'

vi.mock('@/features/dashboard/api', () => ({
  getUserQuotaDataByUsers: vi.fn(),
}))

// Only the network and browser canvas boundaries are replaced.
vi.mock('@visactor/react-vchart', () => ({
  VChart: () => <div aria-label='Chart canvas' />,
}))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: vi.fn() },
}))

let queryClient: QueryClient

beforeEach(() => {
  localStorage.clear()
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.clearAllMocks()
  localStorage.clear()
})

function renderUserCharts() {
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider defaultTheme='light'>
        <UserCharts
          filters={{
            selectedRange: 29,
            timeGranularity: 'day',
            topUserLimit: 10,
          }}
          onFiltersChange={vi.fn()}
        />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

describe('user statistics request failures', () => {
  it.each([
    ['business failure', { success: false, data: [] }],
    ['malformed successful response', { success: true, data: {} }],
  ])(
    'shows a persistent error instead of empty charts for %s',
    async (_, response) => {
      vi.mocked(getUserQuotaDataByUsers).mockResolvedValue(
        response as Awaited<ReturnType<typeof getUserQuotaDataByUsers>>
      )
      renderUserCharts()

      expect(await screen.findByRole('alert')).toHaveTextContent(
        'Request failed'
      )
      expect(screen.queryByLabelText('Chart canvas')).not.toBeInTheDocument()
    }
  )

  it('shows a persistent error when the request rejects', async () => {
    vi.mocked(getUserQuotaDataByUsers).mockRejectedValue(
      new Error('network unavailable')
    )
    renderUserCharts()

    expect(await screen.findByRole('alert')).toHaveTextContent('Request failed')
    expect(screen.queryByLabelText('Chart canvas')).not.toBeInTheDocument()
  })

  it.each([
    { name: 'empty array', data: [] },
    { name: 'null Go slice', data: null },
  ])(
    'keeps a successful $name distinct from a failed request',
    async ({ data }) => {
      vi.mocked(getUserQuotaDataByUsers).mockResolvedValue({
        success: true,
        data,
      } as Awaited<ReturnType<typeof getUserQuotaDataByUsers>>)
      renderUserCharts()

      expect(await screen.findAllByLabelText('Chart canvas')).toHaveLength(2)
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    }
  )
})
