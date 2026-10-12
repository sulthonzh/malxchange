package evm

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/shopspring/decimal"
)

// normalizeLower is the lowercase hex form used for watch matching.
func normalizeLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'F' {
			b[i] += 32
		}
	}
	return string(b)
}

func TestWeiDecimalConversions(t *testing.T) {
	cases := []struct {
		wei *big.Int
		out string
	}{
		{big.NewInt(1e18), "1"},
		{new(big.Int).Mul(big.NewInt(1234), big.NewInt(1e18)), "1234"},
		{big.NewInt(1), "0.000000000000000001"},
	}
	for _, c := range cases {
		if got := weiToDecimal(c.wei).String(); got != c.out {
			t.Errorf("weiToDecimal(%s) = %s, want %s", c.wei, got, c.out)
		}
		back := toUnits(weiToDecimal(c.wei), NativeDecimals)
		if back.Cmp(c.wei) != 0 {
			t.Errorf("round trip %s -> %s", c.wei, back)
		}
	}
	if got := toUnits(decimal.RequireFromString("250.5"), 6).String(); got != "250500000" {
		t.Errorf("toUnits(250.5, 6) = %s, want 250500000", got)
	}
}

func TestParseTransferLog(t *testing.T) {
	token := common.HexToAddress("0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	from := common.HexToAddress("0x000000000000000000000000000000000000dead")
	to := common.HexToAddress("0xAbCdef0000000000000000000000000000000123")
	watch := map[string]string{
		// lowercase key, as ScanBlock builds before calling
		strings.ToLower(to.String()): to.String(),
	}
	value := new(big.Int).SetUint64(250500000) // 250.5 at 6dp
	lg := types.Log{
		Address: token,
		Topics: []common.Hash{
			transferTopic,
			common.BytesToHash(from.Bytes()),
			common.BytesToHash(to.Bytes()),
		},
		Data:   common.LeftPadBytes(value.Bytes(), 32),
		TxHash: common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
		Index:  3,
	}
	d, ok := parseTransferLog(lg, watch, 6, 42)
	if !ok {
		t.Fatal("expected log to match watch")
	}
	if d.Amount.String() != "250.5" {
		t.Errorf("amount = %s, want 250.5", d.Amount)
	}
	if d.Vout != 3 || d.Height != 42 {
		t.Errorf("vout/height = %d/%d, want 3/42", d.Vout, d.Height)
	}
	if d.TxID != lg.TxHash.Hex() {
		t.Errorf("txid = %s", d.TxID)
	}

	bad := lg
	bad.Topics = []common.Hash{common.HexToHash("0xdead"), bad.Topics[1], bad.Topics[2]}
	if _, ok := parseTransferLog(bad, watch, 6, 42); ok {
		t.Error("wrong topic matched")
	}
	if _, ok := parseTransferLog(lg, map[string]string{"0x0000000000000000000000000000000000000001": ""}, 6, 42); ok {
		t.Error("unwatched recipient matched")
	}
	badData := lg
	badData.Data = []byte{1, 2, 3}
	if _, ok := parseTransferLog(badData, watch, 6, 42); ok {
		t.Error("malformed data matched")
	}
}

func TestAddressNormalization(t *testing.T) {
	mixed := "0xAbCdef0000000000000000000000000000000123"
	if normalizeLower(mixed) != strings.ToLower(mixed) {
		t.Errorf("normalizeLower(%s) = %s", mixed, normalizeLower(mixed))
	}
	// ScanBlock lowers watch keys and topic-derived addresses before
	// matching: a checksummed config address still hits.
	lower := map[string]string{normalizeLower(mixed): mixed}
	addr := common.HexToAddress(mixed)
	if _, ok := lower[normalizeLower(addr.String())]; !ok {
		t.Error("checksummed watch key did not match normalized topic address")
	}
}
