package wallet

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/service"
	"github.com/syaikhipin/malxchange/internal/wallet/chain"
	"github.com/syaikhipin/malxchange/internal/wallet/chain/btc"
	"github.com/syaikhipin/malxchange/internal/wallet/chain/evm"
	"github.com/syaikhipin/malxchange/internal/wallet/chain/tron"
	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// Run is the wallet service Extra: the sole Ledger writer boots the
// settlement consumer, per-Coin watchers (deposits credit at the
// confirmation count), the withdrawal/sweep/hot-transfer executors,
// and the internal RPC surface (api + admin-api seams). A settlement
// failure is fatal by design — restart + idempotent redelivery; chain
// failures back off and surface on the Wallet Ops page instead.
func Run(ctx context.Context, d service.Deps) error {
	env := LoadWalletEnv()

	senders, hotAddrs, err := buildSenders(ctx, env, d.Log)
	if err != nil {
		return err
	}

	consumer, err := kafka.NewConsumer(d.Cfg.Kafka, ConsumerGroup, []string{
		events.TopicTradeFill, events.TopicOrderCompleted, events.TopicMemberRegister,
	})
	if err != nil {
		return err
	}
	defer consumer.Close()

	watchCache := newWatchCache(d.Pool)
	wireReconciliation(d.Pool, senders, hotAddrs)
	RegisterRPC(d.Internal, &RPC{DB: d.Pool, Log: d.Log, WatchFor: watchCache.get})

	poll, err := time.ParseDuration(env.PollInterval)
	if err != nil || poll <= 0 {
		poll = 2 * time.Second
	}

	errCh := make(chan error, 3)
	go func() { errCh <- ConsumeLedgerTopics(ctx, d.Pool, d.Log, consumer) }()
	go func() {
		errCh <- RunWatchers(ctx, d.Pool, watcherFactory(ctx, d.Pool, senders, d.Log), d.Log, poll)
	}()
	go func() { errCh <- RunExecutors(ctx, d.Pool, senders, d.Log, poll) }()

	d.Log.Info("wallet running",
		"watchOnly", len(senders.Acct) == 0,
		"families", fmt.Sprintf("btc=%v ltc=%v evm=%v tron=%v",
			senders.UTXO["btc"] != nil, senders.UTXO["ltc"] != nil, senders.EVM != nil, senders.TRON != nil))

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// buildSenders constructs the family chain clients and decrypts the
// hot keystore into memory. Missing config = that family stays in
// watch-only mode (a nil client skips its coins with a logged notice).
func buildSenders(ctx context.Context, env WalletEnv, log *slog.Logger) (*Senders, map[string]string, error) {
	s := &Senders{UTXO: map[string]*btc.RPC{}, Acct: map[string]*keys.Account{}}
	hotAddrs := map[string]string{}
	if env.BtcRPCURL != "" {
		r, err := btc.NewRPC(chain.BTC, env.BtcRPCURL, env.BtcRPCUser, env.BtcRPCPass, env.ChainTLSSkip)
		if err != nil {
			return nil, nil, err
		}
		s.UTXO[chain.BTC] = r
	}
	if env.LtcRPCURL != "" {
		r, err := btc.NewRPC(chain.LTC, env.LtcRPCURL, env.LtcRPCUser, env.LtcRPCPass, env.ChainTLSSkip)
		if err != nil {
			return nil, nil, err
		}
		s.UTXO[chain.LTC] = r
	}
	if env.EvmRPCURL != "" {
		c, err := evm.New(ctx, env.EvmRPCURL, env.EvmFallbackURL)
		if err != nil {
			return nil, nil, fmt.Errorf("evm rpc: %w", err)
		}
		s.EVM = c
	}
	if env.TronEndpoint != "" {
		c, err := tron.New(ctx, env.TronEndpoint, env.TronAPIKey)
		if err != nil {
			// java-tron is Phase-2/optional: watch-only, restart later.
			log.Error("tron endpoint unreachable — TRC-20 watching disabled until restart", "err", err)
		} else {
			s.TRON = c
		}
	}
	if env.KeystorePath != "" && env.Passphrase != "" {
		hk, err := loadHotKeystore(env.KeystorePath, env.Passphrase)
		if err != nil {
			return nil, nil, fmt.Errorf("hot keystore: %w", err)
		}
		for fam, ak := range hk.Accounts {
			acct, err := keys.AccountFromXPrv(fam, WalletNet(), ak.XPrv)
			if err != nil {
				return nil, nil, fmt.Errorf("keystore account %s: %w", fam, err)
			}
			if got, err := acct.HotAddress(); err == nil && got != ak.HotAddress {
				return nil, nil, fmt.Errorf("keystore %s hot address %s != derived %s (wrong net?)", fam, ak.HotAddress, got)
			}
			s.Acct[fam] = acct
			hotAddrs[fam] = ak.HotAddress
			log.Info("hot account loaded", "family", fam, "hotAddress", ak.HotAddress)
		}
	} else {
		log.Warn("no hot keystore — running watch-only (deposits credit; withdrawals/sweeps fail until the key ceremony lands)")
	}
	return s, hotAddrs, nil
}

// watcherFactory maps a Coin to its chain Watcher; setup errors keep
// retrying on the next refresh tick (chains may come up after wallet).
func watcherFactory(ctx context.Context, db *pgxpool.Pool, s *Senders, log *slog.Logger) func(Coin) (*CoinWatcher, error) {
	return func(c Coin) (*CoinWatcher, error) {
		var w chain.Watcher
		switch c.Family {
		case chain.BTC, chain.LTC:
			r, ok := s.UTXO[c.Family]
			if !ok {
				return nil, fmt.Errorf("no %s RPC configured", c.Family)
			}
			w = r
		case chain.EVM:
			if s.EVM == nil {
				return nil, fmt.Errorf("no evm RPC configured")
			}
			if !c.Native && c.ChainDecimals != 0 {
				s.EVM.RegisterToken(c.Contract, c.ChainDecimals)
			}
			w = s.EVM
		case chain.TRON:
			if s.TRON == nil {
				return nil, fmt.Errorf("no tron endpoint configured")
			}
			if !c.Native && c.ChainDecimals != 0 {
				s.TRON.RegisterToken(c.Contract, c.ChainDecimals)
			}
			w = s.TRON
		default:
			return nil, fmt.Errorf("%w: %s", ErrUnknownFamily, c.Family)
		}
		return NewCoinWatcher(ctx, db, c, w, log)
	}
}

// watchCache memoizes coin → *keys.Watch (xpubs change only via the
// key ceremony, but the cache keeps tests and SQL edits honest).
type watchCache struct {
	db  *pgxpool.Pool
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]watchEntry
}

type watchEntry struct {
	w  *keys.Watch
	at time.Time
}

func newWatchCache(db *pgxpool.Pool) *watchCache {
	return &watchCache{db: db, ttl: time.Minute, m: map[string]watchEntry{}}
}

func (c *watchCache) get(ctx context.Context, coin string) (*keys.Watch, error) {
	c.mu.Lock()
	if e, ok := c.m[coin]; ok && time.Since(e.at) < c.ttl {
		c.mu.Unlock()
		return e.w, nil
	}
	c.mu.Unlock()
	co, err := GetCoin(ctx, c.db, coin)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCoin, coin)
	}
	if co.XPub == "" {
		return nil, fmt.Errorf("coin %s has no xpub registered (key ceremony pending)", coin)
	}
	w, err := keys.ImportXPub(co.Family, WatchNet(co), co.XPub)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.m[coin] = watchEntry{w: w, at: time.Now()}
	c.mu.Unlock()
	return w, nil
}

// wireReconciliation plugs the watch-only balance queries into the
// reconcile/ops surfaces.
func wireReconciliation(db *pgxpool.Pool, s *Senders, hotAddrs map[string]string) {
	HotBalanceQuery = func(ctx context.Context, c Coin) (string, bool) {
		switch c.Family {
		case chain.BTC, chain.LTC:
			hot, ok := hotAddrs[c.Family]
			if !ok {
				return "", false
			}
			var sum string
			if err := db.QueryRow(ctx, `
				SELECT COALESCE(SUM(amount),0)::text FROM utxos WHERE coin=$1 AND address=$2 AND NOT spent`,
				c.Name, hot).Scan(&sum); err != nil {
				return "", false
			}
			return sum, true
		case chain.EVM:
			if s.EVM == nil {
				return "", false
			}
			hot, ok := hotAddrs[chain.EVM]
			if !ok {
				return "", false
			}
			if c.Native {
				b, err := s.EVM.HotBalance(ctx, hot)
				return b.String(), err == nil
			}
			b, err := s.EVM.TokenBalance(ctx, c.Contract, hot)
			return b.String(), err == nil
		case chain.TRON:
			if s.TRON == nil {
				return "", false
			}
			hot, ok := hotAddrs[chain.TRON]
			if !ok {
				return "", false
			}
			if c.Native {
				b, err := s.TRON.TRXBalance(ctx, hot)
				return b.String(), err == nil
			}
			b, err := s.TRON.TRC20Balance(ctx, c.Contract, hot)
			return b.String(), err == nil
		}
		return "", false
	}
	AddressBalanceQuery = func(ctx context.Context, c Coin, addr string) (string, bool) {
		switch c.Family {
		case chain.EVM:
			if s.EVM == nil {
				return "", false
			}
			if c.Native {
				b, err := s.EVM.HotBalance(ctx, addr)
				return b.String(), err == nil
			}
			b, err := s.EVM.TokenBalance(ctx, c.Contract, addr)
			return b.String(), err == nil
		case chain.TRON:
			if s.TRON == nil {
				return "", false
			}
			if c.Native {
				b, err := s.TRON.TRXBalance(ctx, addr)
				return b.String(), err == nil
			}
			b, err := s.TRON.TRC20Balance(ctx, c.Contract, addr)
			return b.String(), err == nil
		}
		return "", false
	}
}

// loadHotKeystore reads and decrypts the keystore file into memory.
func loadHotKeystore(path, passphrase string) (keys.HotKeystore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return keys.HotKeystore{}, err
	}
	hk, err := keys.DecryptHot(data, passphrase)
	if err != nil {
		return keys.HotKeystore{}, fmt.Errorf("%w: %s", keys.ErrBadPassphrase, path)
	}
	return hk, nil
}
