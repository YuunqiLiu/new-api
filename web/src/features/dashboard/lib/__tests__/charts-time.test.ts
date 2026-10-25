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
import { describe, expect, test, vi } from 'vitest'

import type { QuotaDataItem } from '../../types'
import { processChartData, processUserChartData } from '../charts'

// Currency is a browser preference; chart aggregation uses the real processors.
vi.mock('@/lib/currency', () => ({
  getCurrencyDisplay: () => ({
    config: { quotaPerUnit: 500_000 },
    meta: { kind: 'currency', exchangeRate: 1, symbol: '$' },
  }),
}))

type ChartRow = {
  Time: string
  rawQuota?: number
  Count?: number
}

function rows(spec: Record<string, unknown>): ChartRow[] {
  return (spec.data as { values: ChartRow[] }[])[0].values
}

function usage(
  year: number,
  month: number,
  day: number,
  quota = 500_000,
  count = 1,
  hour = 12
): QuotaDataItem {
  return {
    created_at: new Date(year, month - 1, day, hour).getTime() / 1000,
    model_name: 'example-model',
    username: 'example-user',
    quota,
    count,
    token_used: count * 100,
  }
}

describe('dashboard charts preserve the dates actually returned by the API', () => {
  test('two returned days do not fabricate five earlier zero days', () => {
    const input = [usage(2026, 10, 17), usage(2026, 10, 18)]
    const models = processChartData(input, 'day')
    const users = processUserChartData(input, 'day')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      models.spec_model_line,
      users.spec_user_trend,
    ]) {
      expect(rows(spec).map((row) => row.Time)).toEqual(['10-17', '10-18'])
    }
  })

  test('sparse usage over 29 days keeps older returned points and their totals', () => {
    const input = [
      usage(2026, 10, 18, 1_500_000, 3),
      usage(2026, 9, 20, 500_000, 1),
      usage(2026, 10, 1, 1_000_000, 2),
    ]
    const models = processChartData(input, 'day')
    const users = processUserChartData(input, 'day')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      users.spec_user_trend,
    ]) {
      expect(rows(spec).map((row) => row.Time)).toEqual([
        '09-20',
        '10-01',
        '10-18',
      ])
      expect(rows(spec).map((row) => row.rawQuota)).toEqual([
        500_000, 1_000_000, 1_500_000,
      ])
    }
    expect(rows(models.spec_model_line).map((row) => row.Count)).toEqual([
      1, 2, 3,
    ])
    expect(models.totalQuotaDisplay).toBe('$6.00')
    expect(models.totalCountDisplay).toBe('6')
  })

  test('a complete 29-day response remains 29 days in every time chart', () => {
    const input = Array.from({ length: 29 }, (_, i) => usage(2026, 10, i + 1))
    const models = processChartData(input, 'day')
    const users = processUserChartData(input, 'day')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      models.spec_model_line,
      users.spec_user_trend,
    ]) {
      expect(rows(spec)).toHaveLength(29)
      expect(rows(spec)[0].Time).toBe('10-01')
      expect(rows(spec)[28].Time).toBe('10-29')
    }
  })

  test('empty API data stays empty instead of presenting synthetic zero consumption', () => {
    const models = processChartData([], 'day')
    const users = processUserChartData([], 'day')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      models.spec_model_line,
      models.spec_pie,
      models.spec_rank_bar,
      users.spec_user_trend,
      users.spec_user_rank,
    ]) {
      expect(rows(spec)).toEqual([])
    }
    expect(models.totalQuotaDisplay).toBe('$0.00')
    expect(models.totalCountDisplay).toBe('0')
  })

  test('day buckets are chronological across New Year and never merge different years', () => {
    const input = [
      usage(2026, 1, 1, 1_500_000, 3),
      usage(2025, 12, 31, 1_000_000, 2),
      usage(2025, 1, 1, 500_000, 1),
    ]
    const models = processChartData(input, 'day')
    const users = processUserChartData(input, 'day')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      users.spec_user_trend,
    ]) {
      expect(rows(spec).map((row) => row.rawQuota)).toEqual([
        500_000, 1_000_000, 1_500_000,
      ])
      expect(new Set(rows(spec).map((row) => row.Time)).size).toBe(3)
    }
    expect(rows(models.spec_model_line).map((row) => row.Count)).toEqual([
      1, 2, 3,
    ])
  })

  test('hour buckets keep chronological order through midnight and New Year', () => {
    const input = [
      usage(2026, 1, 1, 1_000_000, 2, 0),
      usage(2025, 12, 31, 500_000, 1, 23),
    ]
    const models = processChartData(input, 'hour')
    const users = processUserChartData(input, 'hour')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      users.spec_user_trend,
    ]) {
      expect(rows(spec).map((row) => row.rawQuota)).toEqual([
        500_000, 1_000_000,
      ])
    }
    expect(rows(models.spec_model_line).map((row) => row.Count)).toEqual([1, 2])
  })

  test('weekly views combine Monday through Sunday and retain the next Monday', () => {
    const input = [
      usage(2026, 10, 19, 1_500_000, 3),
      usage(2026, 10, 12, 500_000, 1),
      usage(2026, 10, 18, 1_000_000, 2),
    ]
    const models = processChartData(input, 'week')
    const users = processUserChartData(input, 'week')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      users.spec_user_trend,
    ]) {
      expect(rows(spec).map((row) => row.Time)).toEqual([
        '10-12 - 10-18',
        '10-19 - 10-25',
      ])
      expect(rows(spec).map((row) => row.rawQuota)).toEqual([
        1_500_000, 1_500_000,
      ])
    }
    expect(rows(models.spec_model_line).map((row) => row.Count)).toEqual([3, 3])
  })

  test('a week spanning New Year combines both calendar years without losing usage', () => {
    const input = [
      usage(2026, 1, 5, 1_500_000, 3),
      usage(2025, 12, 29, 500_000, 1),
      usage(2026, 1, 4, 1_000_000, 2),
    ]
    const models = processChartData(input, 'week')
    const users = processUserChartData(input, 'week')

    for (const spec of [
      models.spec_line,
      models.spec_area,
      users.spec_user_trend,
    ]) {
      expect(rows(spec).map((row) => row.rawQuota)).toEqual([
        1_500_000, 1_500_000,
      ])
      expect(new Set(rows(spec).map((row) => row.Time)).size).toBe(2)
    }
    expect(rows(models.spec_model_line).map((row) => row.Count)).toEqual([3, 3])
    expect(models.totalQuotaDisplay).toBe('$6.00')
  })
})
