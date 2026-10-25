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
import { useTranslation } from 'react-i18next'

import { formatDateTimeObject } from '@/lib/time'

interface QueryRangeProps {
  start: number
  end: number
}

export function QueryRange(props: QueryRangeProps) {
  const { t } = useTranslation()
  const start = new Date(props.start * 1000)
  const end = new Date(props.end * 1000)

  return (
    <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
      <span>
        {t('Start Time')}:{' '}
        <time dateTime={start.toISOString()}>
          {formatDateTimeObject(start)}
        </time>
      </span>
      <span>
        {t('End Time')}:{' '}
        <time dateTime={end.toISOString()}>{formatDateTimeObject(end)}</time>
      </span>
    </div>
  )
}
