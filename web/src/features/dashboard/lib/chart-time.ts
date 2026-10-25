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
import dayjs from '@/lib/dayjs'
import type { TimeGranularity } from '@/lib/time'

// Use calendar boundaries in the same local timezone as the date controls.
// Weeks consistently start on Monday, independently of the display locale.
export function getChartTimeBucket(
  timestamp: number,
  granularity: TimeGranularity
): number {
  const date = dayjs(timestamp * 1000)
  if (granularity === 'hour') return date.startOf('hour').unix()

  const day = date.startOf('day')
  if (granularity === 'week') {
    return day.subtract((day.day() + 6) % 7, 'day').unix()
  }
  return day.unix()
}

export function formatChartTimeBucket(
  timestamp: number,
  granularity: TimeGranularity,
  includeYear: boolean
): string {
  const date = dayjs(timestamp * 1000)
  const dateFormat = includeYear ? 'YYYY-MM-DD' : 'MM-DD'
  const label = date.format(dateFormat)
  if (granularity === 'hour') return `${label} ${date.format('HH')}:00`
  if (granularity === 'week') {
    return `${label} - ${date.add(6, 'day').format(dateFormat)}`
  }
  return label
}
