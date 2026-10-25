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
import { render, screen, cleanup } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'

import { QueryRange } from '../query-range'

afterEach(cleanup)

test('shows the exact requested dates instead of implying a padded week', () => {
  const start = new Date(2026, 9, 17, 21, 23, 45)
  const end = new Date(2026, 9, 18, 21, 23, 45)
  render(
    <QueryRange start={start.getTime() / 1000} end={end.getTime() / 1000} />
  )

  expect(screen.getByText('2026-10-17 21:23:45')).toBeVisible()
  expect(screen.getByText('2026-10-18 21:23:45')).toBeVisible()
  expect(screen.queryByText(/2026-10-12/)).not.toBeInTheDocument()
})
