import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'

type Bucket = { key: string; prompt_tokens: number; cached_tokens: number; requests: number; cold_starts: number; affinity_hits: number; cross_channel: number }
type Report = { truncated: boolean; overall: Bucket; trend: Bucket[]; by_model: Bucket[]; by_channel: Bucket[]; session_count: number }
const WINDOWS = ['1h', '6h', '24h', '7d', '30d']
const rate = (bucket: Bucket) => bucket.prompt_tokens > 0 ? `${((bucket.cached_tokens / bucket.prompt_tokens) * 100).toFixed(2)}%` : '—'

export function PromptCacheMonitoring() {
  const { t } = useTranslation()
  const [window, setWindow] = useState('24h')
  const [report, setReport] = useState<Report | null>(null)
  useEffect(() => { void api.get('/api/log/prompt_cache_monitoring', { params: { window } }).then((res) => setReport(res.data.data)) }, [window])
  if (!report) return <div>{t('Loading...')}</div>
  return <div className='space-y-4'>
    <div className='flex flex-wrap gap-2'>{WINDOWS.map((value) => <Button key={value} size='sm' variant={window === value ? 'default' : 'outline'} onClick={() => setWindow(value)}>{value}</Button>)}</div>
    {report.truncated && <p className='text-sm text-destructive'>{t('Results are capped at the most recent 100,000 requests.')}</p>}
    <div className='grid gap-3 md:grid-cols-4'>
      <Metric title={t('Cache hit rate')} value={rate(report.overall)} />
      <Metric title={t('Prompt cache tokens')} value={report.overall.cached_tokens.toLocaleString()} />
      <Metric title={t('Cold starts / affinity hits')} value={`${report.overall.cold_starts} / ${report.overall.affinity_hits}`} />
      <Metric title={t('Sessions switching channels')} value={`${report.overall.cross_channel} / ${report.session_count}`} />
    </div>
    <CacheTable title={t('Channel comparison')} rows={report.by_channel} />
    <CacheTable title={t('Model comparison')} rows={report.by_model} />
    <CacheTable title={t('Cache hit trend')} rows={report.trend} />
  </div>
}
function Metric({ title, value }: { title: string; value: string }) { return <Card><CardHeader><CardTitle className='text-sm'>{title}</CardTitle></CardHeader><CardContent className='text-2xl font-semibold'>{value}</CardContent></Card> }
function CacheTable({ title, rows }: { title: string; rows: Bucket[] }) { const { t } = useTranslation(); return <Card><CardHeader><CardTitle>{title}</CardTitle></CardHeader><CardContent><div className='overflow-x-auto'><table className='w-full text-sm'><thead><tr className='text-left text-muted-foreground'><th>{t('Dimension')}</th><th>{t('Prompt tokens')}</th><th>{t('Cached tokens')}</th><th>{t('Cache hit rate')}</th><th>{t('Requests')}</th></tr></thead><tbody>{rows.map((row) => <tr key={row.key} className='border-t'><td className='py-2'>{row.key}</td><td>{row.prompt_tokens.toLocaleString()}</td><td>{row.cached_tokens.toLocaleString()}</td><td>{rate(row)}</td><td>{row.requests.toLocaleString()}</td></tr>)}</tbody></table></div></CardContent></Card> }
