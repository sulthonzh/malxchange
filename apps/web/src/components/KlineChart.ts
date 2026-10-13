// The Kline chart (spec #24 story 25, ADR-0003): TradingView
// lightweight-charts candlesticks fed by kline REST history, updated live
// from the kline WS channels. No user-visible copy lives here
// (ADR-0013 rule 2: nothing baked into charts) — interval labels come
// from the shared contract, tooltips from the library.
//
// The factory is the unit seam: the page owns the library instance so
// the chart never survives its element.
import { createChart, type IChartApi, type ISeriesApi, type UTCTimestamp } from 'lightweight-charts'

import type { KlineBar } from '~/utils/exchange'

export interface KlineChart {
  updateHistory: (bars: KlineBar[], interval: string) => void
  updateFromChannel: (bar: KlineBar) => void
  resize: () => void
  destroy: () => void
}

function toTime(openTime: string): UTCTimestamp {
  const seconds = Math.floor(new Date(openTime).getTime() / 1000)
  return (Number.isFinite(seconds) ? seconds : 0) as UTCTimestamp
}

function toCandle(bar: KlineBar): { time: UTCTimestamp; open: number; high: number; low: number; close: number } {
  return {
    time: toTime(bar.openTime),
    open: Number(bar.open),
    high: Number(bar.high),
    low: Number(bar.low),
    close: Number(bar.close),
  }
}

// ADR-0005 waiver — candles carry display values (not money), so the
// chart converts with Number. All money math stays on decimal strings
// in the page; nothing in this file ever computes an amount.
export function createKlineChart(element: HTMLElement): KlineChart {
  const chart: IChartApi = createChart(element, {
    layout: { background: { color: 'transparent' }, textColor: '#8b93a7' },
    grid: { vertLines: { color: 'rgba(139, 147, 167, 0.12)' }, horzLines: { color: 'rgba(139, 147, 167, 0.12)' } },
    rightPriceScale: { borderColor: 'rgba(139, 147, 167, 0.3)' },
    timeScale: { borderColor: 'rgba(139, 147, 167, 0.3)', timeVisible: true },
  })
  const series: ISeriesApi<'Candlestick'> = chart.addCandlestickSeries({
    upColor: '#00b275',
    downColor: '#f15057',
    wickUpColor: '#00b275',
    wickDownColor: '#f15057',
    borderVisible: false,
  })

  return {
    updateHistory(bars: KlineBar[]): void {
      series.setData(bars.map(toCandle))
      chart.timeScale().fitContent()
    },
    updateFromChannel(bar: KlineBar): void {
      series.update(toCandle(bar))
    },
    resize(): void {
      chart.resize(element.clientWidth, element.clientHeight)
    },
    destroy(): void {
      chart.remove()
    },
  }
}
