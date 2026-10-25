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
import type { UserChartsFilters } from '@/features/dashboard/types'

export const USER_CHARTS_FILTERS_STORAGE_KEY = 'dashboard_user_chart_filters'

const DEFAULT_USER_CHARTS_FILTERS: UserChartsFilters = {
  selectedRange: 1,
  timeGranularity: 'hour',
  topUserLimit: 10,
}

function normalizeUserChartsFilters(value: unknown): UserChartsFilters {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ...DEFAULT_USER_CHARTS_FILTERS }
  }

  const filters = value as Record<string, unknown>
  const selectedRange = filters.selectedRange
  const timeGranularity = filters.timeGranularity
  const topUserLimit = filters.topUserLimit

  return {
    selectedRange:
      typeof selectedRange === 'number' &&
      [1, 7, 14, 29].includes(selectedRange)
        ? selectedRange
        : DEFAULT_USER_CHARTS_FILTERS.selectedRange,
    timeGranularity:
      timeGranularity === 'hour' ||
      timeGranularity === 'day' ||
      timeGranularity === 'week'
        ? timeGranularity
        : DEFAULT_USER_CHARTS_FILTERS.timeGranularity,
    topUserLimit:
      typeof topUserLimit === 'number' && [5, 10, 20, 50].includes(topUserLimit)
        ? topUserLimit
        : DEFAULT_USER_CHARTS_FILTERS.topUserLimit,
  }
}

export function getSavedUserChartsFilters(): UserChartsFilters {
  if (typeof window === 'undefined') return { ...DEFAULT_USER_CHARTS_FILTERS }

  try {
    const raw = localStorage.getItem(USER_CHARTS_FILTERS_STORAGE_KEY)
    if (!raw) return { ...DEFAULT_USER_CHARTS_FILTERS }
    const parsed: unknown = JSON.parse(raw)
    return normalizeUserChartsFilters(parsed)
  } catch {
    return { ...DEFAULT_USER_CHARTS_FILTERS }
  }
}

export function saveUserChartsFilters(filters: UserChartsFilters): void {
  if (typeof window === 'undefined') return

  try {
    localStorage.setItem(
      USER_CHARTS_FILTERS_STORAGE_KEY,
      JSON.stringify(normalizeUserChartsFilters(filters))
    )
  } catch {
    // A blocked or full browser store must not prevent changing the live view.
  }
}
