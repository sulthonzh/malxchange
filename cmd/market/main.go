// Command market projects trade.fill / market.plate into the rebuildable
// derived schema: Ticker, Kline, trades, last-known Depth (issue #23).
package main

import "github.com/syaikhipin/malxchange/internal/service"

func main() { service.Main("market", nil) }
