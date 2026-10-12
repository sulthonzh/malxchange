package btc

import (
	"fmt"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

// DecodeScriptAddr renders the address a pkScript pays, best-effort:
// P2WPKH (the deposit form) plus legacy P2PKH/P2SH for sweeps of
// imported old funds. ok=false means the script matches no known
// form (e.g. OP_RETURN, multisig, P2WSH).
func DecodeScriptAddr(family, net string, pkScript []byte) (string, bool) {
	// P2WPKH: OP_0 <20-byte key hash>.
	if len(pkScript) == 22 && pkScript[0] == 0x00 && pkScript[1] == 0x14 {
		return witnessV0Addr(family, net, pkScript[2:])
	}
	// P2PKH: OP_DUP OP_HASH160 <20> OP_EQUALVERIFY OP_CHECKSIG.
	if len(pkScript) == 25 && pkScript[0] == 0x76 && pkScript[1] == 0xa9 &&
		pkScript[2] == 0x14 && pkScript[23] == 0x88 && pkScript[24] == 0xac {
		return legacyAddr(family, net, 0, pkScript[3:23])
	}
	// P2SH: OP_HASH160 <20> OP_EQUAL.
	if len(pkScript) == 23 && pkScript[0] == 0xa9 && pkScript[22] == 0x87 {
		return legacyAddr(family, net, 1, pkScript[2:22])
	}
	return "", false
}

// witnessV0Addr encodes a witness-v0 program as bech32 with the
// family/network HRP.
func witnessV0Addr(family, net string, program []byte) (string, bool) {
	hrp, err := hrpOf(family, net)
	if err != nil {
		return "", false
	}
	conv, err := bech32.ConvertBits(program, 8, 5, true)
	if err != nil {
		return "", false
	}
	addr, err := bech32.Encode(hrp, append([]byte{0x00}, conv...))
	if err != nil {
		return "", false
	}
	return addr, true
}

// legacyAddr base58-check-encodes a hash with the family/network
// version bytes; kind is 0=P2PKH, 1=P2SH.
func legacyAddr(family, net string, kind int, hash []byte) (string, bool) {
	vers, ok := base58Versions[[2]string{family, net}]
	if !ok {
		return "", false
	}
	return base58.CheckEncode(hash, vers[kind]), true
}

// addrScript is the inverse of DecodeScriptAddr for building
// transactions: it turns a bech32 or base58 address (family/net
// forms) back into its pkScript.
func addrScript(family, net string, addr string) ([]byte, error) {
	hrp, err := hrpOf(family, net)
	if err != nil {
		return nil, err
	}
	if hrpRaw, data, err := bech32.Decode(addr); err == nil {
		if hrpRaw != hrp {
			return nil, fmt.Errorf("address %s: wrong hrp for %s/%s", addr, family, net)
		}
		conv, err := bech32.ConvertBits(data[1:], 5, 8, false)
		if err != nil {
			return nil, err
		}
		if data[0] != 0x00 || len(conv) != 20 {
			return nil, fmt.Errorf("address %s: not witness-v0 key hash", addr)
		}
		// OP_0 <20-byte program>.
		return append([]byte{0x00, 0x14}, conv...), nil
	}
	// Base58 fallback: P2PKH / P2SH by version byte.
	vers, ok := base58Versions[[2]string{family, net}]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrBadFamily, family, net)
	}
	decoded, netVer, err := base58.CheckDecode(addr)
	if err != nil {
		return nil, fmt.Errorf("address %s: %w", addr, err)
	}
	switch netVer {
	case vers[0]: // P2PKH
		script := make([]byte, 0, 25)
		script = append(script, 0x76, 0xa9, 0x14)
		script = append(script, decoded...)
		return append(script, 0x88, 0xac), nil
	case vers[1]: // P2SH
		script := make([]byte, 0, 23)
		script = append(script, 0xa9, 0x14)
		script = append(script, decoded...)
		return append(script, 0x87), nil
	}
	return nil, fmt.Errorf("address %s: unknown version byte 0x%02x", addr, netVer)
}
