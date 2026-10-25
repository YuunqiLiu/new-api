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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  DASHBOARD_CHART_PREFERENCES_STORAGE_KEY,
  TIME_GRANULARITY_STORAGE_KEY,
} from '@/features/dashboard/constants'
import {
  getSavedUserChartsFilters,
  saveUserChartsFilters,
  USER_CHARTS_FILTERS_STORAGE_KEY,
} from '@/features/dashboard/lib/user-chart-filters'

const defaults = {
  selectedRange: 1,
  timeGranularity: 'hour',
  topUserLimit: 10,
} as const

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('saved user statistics filters', () => {
  it('restores a 29-day weekly view with its user limit independently of model defaults', () => {
    const modelDefaults = JSON.stringify({
      defaultTimeRangeDays: 7,
      defaultTimeGranularity: 'day',
    })
    localStorage.setItem(DASHBOARD_CHART_PREFERENCES_STORAGE_KEY, modelDefaults)
    localStorage.setItem(TIME_GRANULARITY_STORAGE_KEY, 'day')
    const selection = {
      selectedRange: 29,
      timeGranularity: 'week',
      topUserLimit: 50,
    } as const

    saveUserChartsFilters(selection)

    expect(getSavedUserChartsFilters()).toEqual(selection)
    expect(localStorage.getItem(DASHBOARD_CHART_PREFERENCES_STORAGE_KEY)).toBe(
      modelDefaults
    )
    expect(localStorage.getItem(TIME_GRANULARITY_STORAGE_KEY)).toBe('day')
  })

  it('starts with independent defaults when only model preferences exist', () => {
    localStorage.setItem(TIME_GRANULARITY_STORAGE_KEY, 'week')
    localStorage.setItem(
      DASHBOARD_CHART_PREFERENCES_STORAGE_KEY,
      JSON.stringify({
        defaultTimeRangeDays: 29,
        defaultTimeGranularity: 'week',
      })
    )

    expect(getSavedUserChartsFilters()).toEqual(defaults)
  })

  it.each(['{broken', 'null', '[]', '42'])(
    'uses safe defaults when stored preferences are not a valid object: %s',
    (stored) => {
      localStorage.setItem(USER_CHARTS_FILTERS_STORAGE_KEY, stored)

      expect(getSavedUserChartsFilters()).toEqual(defaults)
    }
  )

  it('rejects obsolete 30-day ranges without discarding valid granularity or user limits', () => {
    localStorage.setItem(
      USER_CHARTS_FILTERS_STORAGE_KEY,
      JSON.stringify({
        selectedRange: 30,
        timeGranularity: 'week',
        topUserLimit: 20,
      })
    )

    expect(getSavedUserChartsFilters()).toEqual({
      selectedRange: 1,
      timeGranularity: 'week',
      topUserLimit: 20,
    })
  })

  it('rejects unsupported aggregation and user limits while preserving a valid range', () => {
    localStorage.setItem(
      USER_CHARTS_FILTERS_STORAGE_KEY,
      JSON.stringify({
        selectedRange: 14,
        timeGranularity: 'month',
        topUserLimit: 0,
      })
    )

    expect(getSavedUserChartsFilters()).toEqual({
      selectedRange: 14,
      timeGranularity: 'hour',
      topUserLimit: 10,
    })
  })

  it('keeps the live view usable when browser storage is unavailable', () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => {
      throw new Error('storage blocked')
    })
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('storage blocked')
    })

    expect(getSavedUserChartsFilters()).toEqual(defaults)
    expect(() => saveUserChartsFilters(defaults)).not.toThrow()
  })
})
