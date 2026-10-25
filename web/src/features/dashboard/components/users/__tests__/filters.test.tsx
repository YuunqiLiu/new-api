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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ThemeProvider } from '@/context/theme-provider'
import { getUserQuotaDataByUsers } from '@/features/dashboard/api'
import { UserCharts } from '@/features/dashboard/components/users/user-charts'
import { TIME_GRANULARITY_STORAGE_KEY } from '@/features/dashboard/constants'
import {
  getSavedChartPreferences,
  saveChartPreferences,
} from '@/features/dashboard/lib/filters'
import type { UserChartsFilters } from '@/features/dashboard/types'

vi.mock('@/features/dashboard/api', () => ({
  getUserQuotaDataByUsers: vi.fn(),
}))

// Canvas rendering is a browser boundary; the filters, query and chart-data
// transformation remain real so these tests exercise the user interaction.
vi.mock('@visactor/react-vchart', () => ({
  VChart: () => <div aria-label='Chart canvas' />,
}))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: vi.fn() },
}))

const initialFilters: UserChartsFilters = {
  selectedRange: 29,
  timeGranularity: 'hour',
  topUserLimit: 20,
}

const modelPreferences = {
  consumptionDistributionChart: 'area',
  modelAnalyticsChart: 'proportion',
  defaultTimeRangeDays: 14,
  defaultTimeGranularity: 'hour',
} as const

function UserChartsFixture() {
  const [filters, setFilters] = useState<UserChartsFilters>(initialFilters)
  return <UserCharts filters={filters} onFiltersChange={setFilters} />
}

let queryClient: QueryClient

beforeEach(() => {
  localStorage.clear()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-18T12:00:00Z'))
  vi.mocked(getUserQuotaDataByUsers).mockResolvedValue({
    success: true,
    data: [],
  })
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  localStorage.clear()
  vi.clearAllMocks()
  vi.useRealTimers()
})

function renderUserCharts() {
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider defaultTheme='light'>
        <UserChartsFixture />
      </ThemeProvider>
    </QueryClientProvider>
  )
}

describe('user statistics filters', () => {
  it.each(['Day', 'Week'])(
    'keeps the selected 29-day query window when aggregation changes to %s',
    async (granularity) => {
      const user = userEvent.setup()
      renderUserCharts()
      await waitFor(() =>
        expect(getUserQuotaDataByUsers).toHaveBeenCalledTimes(1)
      )
      const originalRange = vi.mocked(getUserQuotaDataByUsers).mock.calls[0][0]
      expect(originalRange.end_timestamp - originalRange.start_timestamp).toBe(
        29 * 24 * 60 * 60
      )

      await user.click(screen.getByRole('tab', { name: granularity }))

      expect(screen.getByRole('tab', { name: '29 Days' })).toHaveAttribute(
        'aria-selected',
        'true'
      )
      expect(screen.getByRole('tab', { name: granularity })).toHaveAttribute(
        'aria-selected',
        'true'
      )
      expect(getUserQuotaDataByUsers).toHaveBeenCalledTimes(1)
      expect(vi.mocked(getUserQuotaDataByUsers).mock.calls[0][0]).toEqual(
        originalRange
      )
    }
  )

  it('does not change saved model chart defaults when user aggregation changes', async () => {
    saveChartPreferences(modelPreferences)
    localStorage.setItem(TIME_GRANULARITY_STORAGE_KEY, 'hour')
    const user = userEvent.setup()
    renderUserCharts()

    await user.click(screen.getByRole('tab', { name: 'Day' }))

    expect(getSavedChartPreferences()).toEqual(modelPreferences)
    expect(localStorage.getItem(TIME_GRANULARITY_STORAGE_KEY)).toBe('hour')
  })

  it('shows the same exact dates as the request after the selected range changes', async () => {
    const user = userEvent.setup()
    renderUserCharts()
    await waitFor(() =>
      expect(getUserQuotaDataByUsers).toHaveBeenCalledTimes(1)
    )

    await user.click(screen.getByRole('tab', { name: '7 Days' }))
    await waitFor(() =>
      expect(getUserQuotaDataByUsers).toHaveBeenCalledTimes(2)
    )

    const requestedRange = vi.mocked(getUserQuotaDataByUsers).mock.calls[1][0]
    expect(requestedRange.end_timestamp - requestedRange.start_timestamp).toBe(
      7 * 24 * 60 * 60
    )
    expect(
      screen
        .getAllByRole('time')
        .map((element) => element.getAttribute('datetime'))
    ).toEqual([
      new Date(requestedRange.start_timestamp * 1000).toISOString(),
      new Date(requestedRange.end_timestamp * 1000).toISOString(),
    ])
  })
})
