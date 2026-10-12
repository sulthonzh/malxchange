package tron

// STUB — TEST INFRASTRUCTURE, NOT A CHAIN.
//
// StubNode is a fake java-tron gRPC node with an in-memory ledger. It
// exists because TRON has no lightweight devnet: the watcher tests of
// spec #22 run against this stub. IT DOES NOT VALIDATE SIGNATURES —
// every broadcast is applied as if perfectly signed. It also does not
// run contracts, track fees/energy, or reorg. NEVER point it at
// anything reachable by untrusted parties.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fbsobreira/gotron-sdk/pkg/common"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/api"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// StubConfig seeds a StubNode.
type StubConfig struct {
	// Tumbler is the pre-funded address tests draw funds from
	// (base58check).
	Tumbler string
	// TRXFund is the tumbler's opening balance in sun (1e-6 TRX).
	TRXFund int64
	// Token is an optional default TRC-20 contract (base58check).
	Token string
	// TokenDecimals is the token's reported decimals().
	TokenDecimals int32
	// TokenFund is the tumbler's opening token balance in base units.
	TokenFund *big.Int
}

// DefaultStubToken is mainnet USDT (6 dp) — the default registry token.
const DefaultStubToken = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

// StubNode is the fake java-tron: a Wallet-service gRPC server over an
// in-memory ledger, plus an admin HTTP endpoint for staging funds.
type StubNode struct {
	cfg StubConfig

	mu        sync.Mutex
	blocks    []*core.Block
	mempool   []*core.Transaction
	txBlock   map[string]int64               // txid hex -> inclusion height
	trx       map[string]int64               // addr base58 -> sun
	tokens    map[string]map[string]*big.Int // contract -> holder -> base units
	decimals  map[string]int32
	autoMine  bool
	grpcSrv   *grpc.Server
	grpcLis   net.Listener
	httpLis   net.Listener
	httpSrv   *http.Server
	stopAuto  chan struct{}
	closeOnce sync.Once
}

// NewStub builds the node with the genesis block and the seeded funds.
func NewStub(cfg StubConfig) *StubNode {
	n := &StubNode{
		cfg:      cfg,
		txBlock:  map[string]int64{},
		trx:      map[string]int64{cfg.Tumbler: cfg.TRXFund},
		tokens:   map[string]map[string]*big.Int{},
		decimals: map[string]int32{},
		autoMine: true,
		stopAuto: make(chan struct{}),
	}
	if cfg.Token != "" {
		n.decimals[cfg.Token] = cfg.TokenDecimals
		if cfg.TokenFund != nil && cfg.TokenFund.Sign() > 0 {
			n.tokens[cfg.Token] = map[string]*big.Int{cfg.Tumbler: new(big.Int).Set(cfg.TokenFund)}
		}
	}
	n.blocks = append(n.blocks, &core.Block{
		BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{Number: 0, Timestamp: time.Now().UnixMilli()}},
	})
	return n
}

// Start binds the gRPC Wallet service on grpcAddr and the admin HTTP
// endpoint on httpAddr ("127.0.0.1:0" picks ephemeral ports). A
// background ticker mines whenever transactions are pending and
// auto-mining is enabled.
func (n *StubNode) Start(grpcAddr, httpAddr string) error {
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}
	n.grpcLis = lis
	n.grpcSrv = grpc.NewServer()
	api.RegisterWalletServer(n.grpcSrv, &stubWallet{node: n})
	go func() { _ = n.grpcSrv.Serve(lis) }()

	hlis, err := net.Listen("tcp", httpAddr)
	if err != nil {
		n.grpcSrv.Stop()
		n.grpcLis = nil
		return err
	}
	n.httpLis = hlis
	mux := http.NewServeMux()
	mux.HandleFunc("/stub/mine", n.handleMine)
	mux.HandleFunc("/stub/transfer", n.handleTransfer)
	n.httpSrv = &http.Server{Handler: mux}
	go func() { _ = n.httpSrv.Serve(hlis) }()

	go func() {
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if n.autoMine {
					n.MineIfPending()
				}
			case <-n.stopAuto:
				return
			}
		}
	}()
	return nil
}

// SetAutoMine toggles broadcast-triggered mining (tests that need
// deterministic heights turn it off and mine via Mine/HTTP).
func (n *StubNode) SetAutoMine(on bool) {
	n.mu.Lock()
	n.autoMine = on
	n.mu.Unlock()
}

// GRPCAddr is the host:port the chain client dials.
func (n *StubNode) GRPCAddr() string { return n.grpcLis.Addr().String() }

// HTTPAddr is the admin endpoint host:port.
func (n *StubNode) HTTPAddr() string { return n.httpLis.Addr().String() }

// Close stops both servers.
func (n *StubNode) Close() {
	n.closeOnce.Do(func() {
		close(n.stopAuto)
		if n.grpcSrv != nil {
			n.grpcSrv.Stop()
		}
		if n.httpSrv != nil {
			_ = n.httpSrv.Close()
		}
	})
}

// Mine appends n blocks, flushing any pending mempool transactions
// into the first one.
func (n *StubNode) Mine(nBlocks int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for range nBlocks {
		n.mineLocked()
	}
}

// MineIfPending mines a single block if transactions are pending.
func (n *StubNode) MineIfPending() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.mempool) > 0 {
		n.mineLocked()
	}
}

// mineLocked appends one block; caller holds n.mu.
func (n *StubNode) mineLocked() {
	tip := n.blocks[len(n.blocks)-1]
	height := tip.GetBlockHeader().GetRawData().GetNumber() + 1
	block := &core.Block{
		BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{
			Number:    height,
			Timestamp: time.Now().UnixMilli(),
		}},
	}
	for _, tx := range n.mempool {
		block.Transactions = append(block.Transactions, tx)
		n.txBlock[TxID(tx)] = height
	}
	n.mempool = nil
	n.blocks = append(n.blocks, block)
}

// RegisterToken adds a token the stub ledger tracks.
func (n *StubNode) RegisterToken(contract string, decimals int32, mint *big.Int, to string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.decimals[contract] = decimals
	if n.tokens[contract] == nil {
		n.tokens[contract] = map[string]*big.Int{}
	}
	if mint != nil && mint.Sign() > 0 {
		cur := n.tokens[contract][to]
		if cur == nil {
			cur = new(big.Int)
			n.tokens[contract][to] = cur
		}
		cur.Add(cur, mint)
	}
}

// Transfer stages tumbler→to movements without signing and mines one
// block containing them: sun is TRX (0 skips), tokenAmount is base
// units of cfg.Token (nil skips).
func (n *StubNode) Transfer(to string, sun int64, tokenAmount *big.Int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.transferLocked(to, sun, tokenAmount)
}

func (n *StubNode) transferLocked(to string, sun int64, tokenAmount *big.Int) error {
	if sun > 0 {
		tc, err := n.transferContract(n.cfg.Tumbler, to, sun)
		if err != nil {
			return err
		}
		if err := n.apply(tc); err != nil {
			return err
		}
		n.mempool = append(n.mempool, tc)
	}
	if tokenAmount != nil && tokenAmount.Sign() > 0 && n.cfg.Token != "" {
		tx, err := n.trc20TransferTx(n.cfg.Tumbler, to, tokenAmount)
		if err != nil {
			return err
		}
		if err := n.apply(tx); err != nil {
			return err
		}
		n.mempool = append(n.mempool, tx)
	}
	n.mineLocked()
	return nil
}

// transferContract builds a native-transfer transaction.
func (n *StubNode) transferContract(from, to string, sun int64) (*core.Transaction, error) {
	owner, err := common.DecodeCheck(from)
	if err != nil {
		return nil, err
	}
	dest, err := common.DecodeCheck(to)
	if err != nil {
		return nil, err
	}
	param, err := anypb.New(&core.TransferContract{OwnerAddress: owner, ToAddress: dest, Amount: sun})
	if err != nil {
		return nil, err
	}
	return n.wrapTx(&core.Transaction_Contract{
		Type: core.Transaction_Contract_TransferContract, Parameter: param,
	}), nil
}

// trc20TransferTx builds a transfer(address,uint256) call tx.
func (n *StubNode) trc20TransferTx(from, to string, units *big.Int) (*core.Transaction, error) {
	owner, err := common.DecodeCheck(from)
	if err != nil {
		return nil, err
	}
	dest, err := common.DecodeCheck(to)
	if err != nil || len(dest) != 21 {
		return nil, fmt.Errorf("stub: bad address %q", to)
	}
	contract, err := common.DecodeCheck(n.cfg.Token)
	if err != nil {
		return nil, err
	}
	data := make([]byte, 0, 68)
	data = append(data, trc20TransferMethod...)
	data = append(data, pad32(dest[1:])...)
	var amt [32]byte
	units.FillBytes(amt[:])
	data = append(data, amt[:]...)
	param, err := anypb.New(&core.TriggerSmartContract{
		OwnerAddress: owner, ContractAddress: contract, Data: data,
	})
	if err != nil {
		return nil, err
	}
	return n.wrapTx(&core.Transaction_Contract{
		Type: core.Transaction_Contract_TriggerSmartContract, Parameter: param,
	}), nil
}

func (n *StubNode) wrapTx(c *core.Transaction_Contract) *core.Transaction {
	num := n.blocks[len(n.blocks)-1].GetBlockHeader().GetRawData().GetNumber()
	return &core.Transaction{RawData: &core.TransactionRaw{
		RefBlockBytes: []byte{byte(num >> 8), byte(num)},
		RefBlockNum:   num,
		Timestamp:     time.Now().UnixMilli(),
		Contract:      []*core.Transaction_Contract{c},
	}}
}

// apply mutates the ledger for every contract in tx; caller holds n.mu.
func (n *StubNode) apply(tx *core.Transaction) error {
	for _, ct := range tx.GetRawData().GetContract() {
		switch ct.GetType() {
		case core.Transaction_Contract_TransferContract:
			tc := new(core.TransferContract)
			if err := ct.GetParameter().UnmarshalTo(tc); err != nil {
				return status.Error(codes.InvalidArgument, "bad TransferContract")
			}
			from := common.EncodeCheck(tc.GetOwnerAddress())
			to := common.EncodeCheck(tc.GetToAddress())
			if n.trx[from] < tc.GetAmount() {
				return fmt.Errorf("stub: insufficient TRX: %s has %d, needs %d", from, n.trx[from], tc.GetAmount())
			}
			n.trx[from] -= tc.GetAmount()
			n.trx[to] += tc.GetAmount()
		case core.Transaction_Contract_TriggerSmartContract:
			sc := new(core.TriggerSmartContract)
			if err := ct.GetParameter().UnmarshalTo(sc); err != nil {
				return status.Error(codes.InvalidArgument, "bad TriggerSmartContract")
			}
			contract := common.EncodeCheck(sc.GetContractAddress())
			if _, ok := n.decimals[contract]; !ok {
				return fmt.Errorf("stub: unknown token contract %s", contract)
			}
			to, amt, ok := parseTRC20Transfer(sc.GetData())
			if !ok {
				return fmt.Errorf("stub: unsupported contract call (only transfer(address,uint256))")
			}
			units := amt.BigInt()
			from := common.EncodeCheck(sc.GetOwnerAddress())
			holders := n.tokens[contract]
			cur := new(big.Int)
			if holders[from] != nil {
				cur.Set(holders[from])
			}
			if cur.Cmp(units) < 0 {
				return fmt.Errorf("stub: insufficient token balance: %s has %s, needs %s", from, cur, units)
			}
			cur.Sub(cur, units)
			if cur.Sign() == 0 {
				delete(holders, from)
			} else {
				holders[from] = cur
			}
			if holders[to] == nil {
				holders[to] = new(big.Int)
			}
			holders[to].Add(holders[to], units)
		default:
			return fmt.Errorf("stub: contract type %v not supported", ct.GetType())
		}
	}
	return nil
}

// broadcast enqueues a transaction into the mempool, applying the
// ledger immediately so balances are consistent; inclusion happens at
// the next mine. Auto-mining (if enabled) mines right away.
func (n *StubNode) broadcast(tx *core.Transaction) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.apply(tx); err != nil {
		return err
	}
	n.mempool = append(n.mempool, tx)
	if n.autoMine {
		n.mineLocked()
	}
	return nil
}

// --- admin HTTP ---

func (n *StubNode) handleMine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query().Get("n")
	count := 1
	if q != "" {
		if _, err := fmt.Sscanf(q, "%d", &count); err != nil || count < 1 {
			http.Error(w, "bad n", http.StatusBadRequest)
			return
		}
	}
	n.Mine(count)
	w.WriteHeader(http.StatusNoContent)
}

// adminTransfer is the /stub/transfer JSON body.
type adminTransfer struct {
	To     string `json:"to"`
	TRX    string `json:"trx"`    // whole TRX, decimal string
	Token  string `json:"token"`  // optional contract override
	Amount string `json:"amount"` // whole token units, decimal string
}

func (n *StubNode) handleTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req adminTransfer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var sun int64
	if strings.TrimSpace(req.TRX) != "" {
		d, err := decimalParse(req.TRX)
		if err != nil {
			http.Error(w, "bad trx: "+err.Error(), http.StatusBadRequest)
			return
		}
		sun = d.Shift(6).IntPart()
	}
	var tokenAmount *big.Int
	if strings.TrimSpace(req.Amount) != "" {
		d, err := decimalParse(req.Amount)
		if err != nil {
			http.Error(w, "bad amount: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Token != "" && req.Token != n.cfg.Token {
			http.Error(w, "token override not supported by admin transfer", http.StatusBadRequest)
			return
		}
		tokenAmount = d.Shift(n.cfg.TokenDecimals).BigInt()
	}
	n.mu.Lock()
	err := n.transferLocked(req.To, sun, tokenAmount)
	n.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decimalParse(s string) (decimal.Decimal, error) {
	return decimal.NewFromString(strings.TrimSpace(s))
}

// --- gRPC Wallet surface ---

type stubWallet struct {
	api.UnimplementedWalletServer
	node *StubNode
}

var _ api.WalletServer = (*stubWallet)(nil)

func (s *stubWallet) GetNowBlock2(ctx context.Context, _ *api.EmptyMessage) (*api.BlockExtention, error) {
	s.node.mu.Lock()
	defer s.node.mu.Unlock()
	return s.node.blockExtLocked(len(s.node.blocks) - 1), nil
}

func (s *stubWallet) GetBlockByNum2(ctx context.Context, m *api.NumberMessage) (*api.BlockExtention, error) {
	s.node.mu.Lock()
	defer s.node.mu.Unlock()
	if m.GetNum() < 0 || int(m.GetNum()) >= len(s.node.blocks) {
		return nil, status.Errorf(codes.NotFound, "block %d not found", m.GetNum())
	}
	return s.node.blockExtLocked(int(m.GetNum())), nil
}

func (s *stubWallet) GetAccount(ctx context.Context, a *core.Account) (*core.Account, error) {
	s.node.mu.Lock()
	defer s.node.mu.Unlock()
	addr := common.EncodeCheck(a.GetAddress())
	bal, ok := s.node.trx[addr]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "account %s not found", addr)
	}
	return &core.Account{Address: a.GetAddress(), Balance: bal}, nil
}

func (s *stubWallet) GetTransactionById(ctx context.Context, m *api.BytesMessage) (*core.Transaction, error) {
	s.node.mu.Lock()
	defer s.node.mu.Unlock()
	id := hex.EncodeToString(m.GetValue())
	h, ok := s.node.txBlock[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "tx %s not found", id)
	}
	for _, tx := range s.node.blocks[h].GetTransactions() {
		if TxID(tx) == id {
			return tx, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "tx %s not found", id)
}

// BroadcastTransaction accepts the tx WITHOUT any signature check.
func (s *stubWallet) BroadcastTransaction(ctx context.Context, tx *core.Transaction) (*api.Return, error) {
	// LOUD: this is a stub. Signature validation is deliberately absent.
	fmt.Printf("STUB: transaction %s accepted — signature NOT validated\n", TxID(tx))
	if err := s.node.broadcast(tx); err != nil {
		return &api.Return{Result: false, Code: api.Return_CONTRACT_VALIDATE_ERROR, Message: []byte(err.Error())}, nil
	}
	return &api.Return{Result: true, Code: api.Return_SUCCESS}, nil
}

// TriggerConstantContract answers decimals() and balanceOf() from the
// ledger without executing real TVM code.
func (s *stubWallet) TriggerConstantContract(ctx context.Context, tsc *core.TriggerSmartContract) (*api.TransactionExtention, error) {
	s.node.mu.Lock()
	defer s.node.mu.Unlock()
	contract := common.EncodeCheck(tsc.GetContractAddress())
	data := tsc.GetData()
	if len(data) < 4 {
		return nil, status.Error(codes.InvalidArgument, "no calldata")
	}
	switch {
	case string(data[:4]) == "\x31\x3c\xe5\x67": // decimals()
		dp, ok := s.node.decimals[contract]
		if !ok {
			return nil, status.Errorf(codes.NotFound, "unknown contract %s", contract)
		}
		out := make([]byte, 32)
		out[31] = byte(dp)
		return &api.TransactionExtention{ConstantResult: [][]byte{out}, Result: &api.Return{Result: true}}, nil
	case string(data[:4]) == "\x70\xa0\x82\x31": // balanceOf(address)
		holders, ok := s.node.tokens[contract]
		if !ok {
			return nil, status.Errorf(codes.NotFound, "unknown contract %s", contract)
		}
		var b [32]byte
		holder := append([]byte{0x41}, data[16:36]...)
		if bal := holders[common.EncodeCheck(holder)]; bal != nil {
			bal.FillBytes(b[:])
		}
		return &api.TransactionExtention{ConstantResult: [][]byte{b[:]}, Result: &api.Return{Result: true}}, nil
	default:
		return nil, status.Error(codes.Unimplemented, "stub: selector not supported")
	}
}

// blockExtLocked converts a stored block to the extention form the
// gotron-sdk client reads; caller holds n.mu.
func (n *StubNode) blockExtLocked(i int) *api.BlockExtention {
	b := n.blocks[i]
	raw, _ := proto.Marshal(b.GetBlockHeader().GetRawData())
	sum := sha256.Sum256(raw)
	be := &api.BlockExtention{BlockHeader: b.GetBlockHeader(), Blockid: sum[:]}
	for _, tx := range b.GetTransactions() {
		id, _ := hex.DecodeString(TxID(tx))
		be.Transactions = append(be.Transactions, &api.TransactionExtention{
			Transaction: tx, Txid: id, Result: &api.Return{Result: true},
		})
	}
	return be
}
