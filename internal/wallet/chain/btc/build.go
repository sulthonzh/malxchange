package btc

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"github.com/syaikhipin/malxchange/internal/wallet/keys"
)

// dustFloor is the minimum change output we are willing to emit
// (btcd's default 300 sat P2WPKH dust threshold). Below it the change
// is deliberately left in the fee rather than creating an output the
// node would reject or that costs more to sweep than it carries.
const dustFloor = 300

// defaultFeeSatPerVByte is the FeeRate fallback when the node cannot
// estimate (young simnet chains have no fee statistics yet).
const defaultFeeSatPerVByte = 10

// Utxo is one spendable P2WPKH output; Branch/Idx locate the key via
// keys.Account.KeyAt.
type Utxo struct {
	TxID     string
	Vout     uint32
	Satoshis int64
	Branch   uint32
	Idx      uint32
}

// Output is one payment destination of BuildSign.
type Output struct {
	Address  string
	Satoshis int64
}

// BuiltTx is a signed, broadcast-ready transaction.
type BuiltTx struct {
	RawHex string
	TxID   string
	// FeeSat is the actual fee: input sum minus output sum (includes
	// any sub-dust change deliberately left as fee).
	FeeSat int64
	Vsize  int64
}

// BuildSign assembles a P2WPKH transaction spending inputs to
// outputs, adds a change output to changeAddress when the remainder
// clears the dust floor, signs every input locally (BIP-143 via
// txscript), and returns the raw hex plus actual fee and vsize. It
// performs no RPC calls — all amounts are exact satoshis.
func (r *RPC) BuildSign(ctx context.Context, acct *keys.Account, inputs []Utxo, outputs []Output, feeSatPerVByte int64, changeAddress string) (*BuiltTx, error) {
	net, err := r.resolveNet(ctx)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 || len(outputs) == 0 {
		return nil, fmt.Errorf("build %s: need inputs and outputs", r.family)
	}
	if feeSatPerVByte < 1 {
		feeSatPerVByte = 1
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	var (
		inSum    int64
		prevOuts = make([]*wire.TxOut, 0, len(inputs))
		pubs     = make([][]byte, 0, len(inputs))
	)
	for _, ut := range inputs {
		if ut.Satoshis <= 0 {
			return nil, fmt.Errorf("build %s: utxo %s/%d: non-positive amount", r.family, ut.TxID, ut.Vout)
		}
		prevHash, err := chainhash.NewHashFromStr(ut.TxID)
		if err != nil {
			return nil, fmt.Errorf("build %s: utxo txid %s: %w", r.family, ut.TxID, err)
		}
		_, pub, err := keyAt(acct, ut.Branch, ut.Idx)
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", r.family, err)
		}
		inSum += ut.Satoshis
		tx.AddTxIn(&wire.TxIn{
			PreviousOutPoint: wire.OutPoint{Hash: *prevHash, Index: ut.Vout},
			Sequence:         0xfffffffd, // RBF-able
		})
		// The witness signs against the P2WPKH script of the KEY
		// that owns the utxo — deposits always pay exactly that.
		prevOuts = append(prevOuts, wire.NewTxOut(ut.Satoshis, p2wpkhScript(pub)))
		pubs = append(pubs, pub)
	}

	var outSum int64
	for _, out := range outputs {
		if out.Satoshis <= 0 {
			return nil, fmt.Errorf("build %s: output %s: amount must be positive", r.family, out.Address)
		}
		pkScript, err := addrScript(r.family, net, out.Address)
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", r.family, err)
		}
		outSum += out.Satoshis
		tx.AddTxOut(wire.NewTxOut(out.Satoshis, pkScript))
	}
	if outSum >= inSum {
		return nil, fmt.Errorf("build %s: outputs %d >= inputs %d", r.family, outSum, inSum)
	}

	// vsize estimate for the fee: standard p2wpkh math (68 vB per
	// input, 31 per output incl. change, 11 overhead) — accurate
	// because every input and the change output are p2wpkh.
	vsize := int64(11 + 68*len(inputs) + 31*(len(outputs)+1))
	fee := vsize * feeSatPerVByte
	if fee >= inSum-outSum {
		return nil, fmt.Errorf("build %s: fee %d exceeds remainder %d", r.family, fee, inSum-outSum)
	}
	if change := inSum - outSum - fee; change >= dustFloor {
		// A sub-dust remainder instead stays in the fee (documented:
		// an uneconomic change output costs more to sweep than it
		// carries); FeeSat below reports the actual difference.
		changeScript, err := addrScript(r.family, net, changeAddress)
		if err != nil {
			return nil, fmt.Errorf("build %s: change: %w", r.family, err)
		}
		tx.AddTxOut(wire.NewTxOut(change, changeScript))
	}

	// Sign: BIP-143 sighashes via txscript, one witness per input.
	sigHashes := txscript.NewTxSigHashes(tx, utxoFetcher(tx, prevOuts))
	for i := range inputs {
		priv, _, err := keyAt(acct, inputs[i].Branch, inputs[i].Idx)
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", r.family, err)
		}
		wit, err := txscript.WitnessSignature(tx, sigHashes, i, inputs[i].Satoshis,
			p2wpkhScript(pubs[i]), txscript.SigHashAll, priv, true)
		if err != nil {
			return nil, fmt.Errorf("build %s: sign input %d: %w", r.family, i, err)
		}
		tx.TxIn[i].Witness = wit
	}

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("build %s: serialize: %w", r.family, err)
	}
	return &BuiltTx{
		RawHex: hex.EncodeToString(buf.Bytes()),
		TxID:   tx.TxHash().String(),
		FeeSat: inSum - outSumOf(tx),
		Vsize:  vsizeOf(tx),
	}, nil
}

// Broadcast submits a signed raw tx, returning its txid.
func (r *RPC) Broadcast(ctx context.Context, rawHex string) (string, error) {
	var txid string
	if err := r.call(ctx, "sendrawtransaction", []any{rawHex}, &txid); err != nil {
		return "", err
	}
	return txid, nil
}

// FeeRate returns the sat/vByte rate from estimatesmartfee(2). The
// node answers in coin/kB; a RATE (not an amount) parsed as float is
// fine — coin/kB × 1e5 is exactly sat/vByte — and young simnets
// without fee statistics fall back to the default rate.
func (r *RPC) FeeRate(ctx context.Context) (int64, error) {
	var est struct {
		FeeRate float64 `json:"feerate"`
	}
	if err := r.call(ctx, "estimatesmartfee", []any{2}, &est); err != nil || est.FeeRate <= 0 {
		return defaultFeeSatPerVByte, nil
	}
	satPerVByte := int64(est.FeeRate * 1e5)
	if satPerVByte < 1 {
		satPerVByte = 1
	}
	return satPerVByte, nil
}

// keyAt derives (priv, compressed pub) at branch/idx.
func keyAt(acct *keys.Account, branch, idx uint32) (*btcec.PrivateKey, []byte, error) {
	raw, err := acct.KeyAt(branch, idx)
	if err != nil {
		return nil, nil, fmt.Errorf("derive key %d/%d: %w", branch, idx, err)
	}
	priv, _ := btcec.PrivKeyFromBytes(raw)
	return priv, priv.PubKey().SerializeCompressed(), nil
}

// p2wpkhScript builds OP_0 <hash160(pub)> for a compressed key.
func p2wpkhScript(compressedPub []byte) []byte {
	script := make([]byte, 0, 22)
	return append(append(script, 0x00, 0x14), btcutil.Hash160(compressedPub)...)
}

func outSumOf(tx *wire.MsgTx) int64 {
	var sum int64
	for _, out := range tx.TxOut {
		sum += out.Value
	}
	return sum
}

// vsizeOf computes ceil(weight/4) from the real serialized sizes.
func vsizeOf(tx *wire.MsgTx) int64 {
	base := int64(tx.SerializeSizeStripped())
	total := int64(tx.SerializeSize())
	return (base*3 + total + 3) / 4
}

// utxoFetcherT answers each input's prevout by outpoint — the
// PrevOutputFetcher the BIP-143 sighash computation gets amounts from.
type utxoFetcherT struct {
	tx   *wire.MsgTx
	outs []*wire.TxOut
}

func utxoFetcher(tx *wire.MsgTx, outs []*wire.TxOut) txscript.PrevOutputFetcher {
	return &utxoFetcherT{tx: tx, outs: outs}
}

func (f *utxoFetcherT) FetchPrevOutput(op wire.OutPoint) *wire.TxOut {
	for i, in := range f.tx.TxIn {
		if in.PreviousOutPoint == op && i < len(f.outs) {
			return f.outs[i]
		}
	}
	return nil
}
