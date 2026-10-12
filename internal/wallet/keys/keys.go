// Package keys implements the custody derivation of ADR-0008: an
// offline ceremony generates one master seed per chain family; the
// host sees watch-only xpubs for deposit-address derivation, while the
// hot keystore holds the account-level extended private keys encrypted
// under the env passphrase (XChaCha20-Poly1305), decrypted only in the
// signer's memory.
//
// Paths (account level; deposits are external-branch children m/0/i,
// the hot address is the change-branch m/1/0):
//
//	btc:  m/84'/0'/0'   (BIP-84 native SegWit)
//	ltc:  m/84'/2'/0'
//	evm:  m/44'/60'/0'  (ETH and ERC-20 share the address)
//	tron: m/44'/195'/0' (TRC-20 on its own path)
package keys

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcutil"
	base58 "github.com/btcsuite/btcd/btcutil/base58"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/common"
	etcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
)

// Families (mirrors internal/wallet/chain; kept local so keys has no
// upward dependencies).
const (
	BTC  = "btc"
	LTC  = "ltc"
	EVM  = "evm"
	TRON = "tron"
)

// Networks. The ceremony runs on mainnet; simnet arms the compose test
// profile chains.
const (
	MainNet = "mainnet"
	SimNet  = "simnet"
)

var (
	// ErrBadPassphrase is a keystore decrypt failure.
	ErrBadPassphrase = errors.New("keystore passphrase is wrong")
	// ErrUnknownFamily is a derivation request outside the four v1
	// chain families.
	ErrUnknownFamily = errors.New("unknown chain family")
)

// NewMnemonic generates ceremony entropy as a BIP-39 mnemonic (bits
// must be 128/192/256).
func NewMnemonic(bits int) (string, error) {
	ent, err := bip39.NewEntropy(bits)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(ent)
}

// Seed turns the ceremony mnemonic into the master seed (BIP-39
// passphrase may add a 25th word; usually empty).
func Seed(mnemonic, passphrase string) ([]byte, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, errors.New("invalid BIP-39 mnemonic")
	}
	return bip39.NewSeed(mnemonic, passphrase), nil
}

// segwitHRP is the native SegWit bech32 HRP per family and network.
// LTC transactions are byte-identical to BTC's (same witness program
// form); only the HRP differs, so ltcd is not linked in.
var segwitHRP = map[[2]string]string{
	{BTC, MainNet}: "bc",
	{BTC, SimNet}:  "sb",
	{LTC, MainNet}: "ltc",
	{LTC, SimNet}:  "sltc",
}

func paramsFor(net string) *chaincfg.Params {
	if net == SimNet {
		return &chaincfg.SimNetParams
	}
	return &chaincfg.MainNetParams
}

// coinType is the BIP-44/84 SLIP-44 coin type of each family.
var coinType = map[string]uint32{BTC: 0, LTC: 2, EVM: 60, TRON: 195}

func purpose(family string) uint32 {
	if family == BTC || family == LTC {
		return 84 // native SegWit
	}
	return 44
}

func accountPath(family string) string {
	return fmt.Sprintf("m/%d'/%d'/0'", purpose(family), coinType[family])
}

// Account is the account-level extended private key of one family —
// the material the offline ceremony derives and the hot keystore
// stores. Never leave this on the host unencrypted.
type Account struct {
	family string
	net    string
	key    *hdkeychain.ExtendedKey
}

// DeriveAccount derives m/<purpose>'/<coin>'/0' from the ceremony seed.
func DeriveAccount(seed []byte, family, net string) (*Account, error) {
	master, err := hdkeychain.NewMaster(seed, paramsFor(net))
	if err != nil {
		return nil, err
	}
	key := master
	for _, idx := range []uint32{hdkeychain.HardenedKeyStart + purpose(family),
		hdkeychain.HardenedKeyStart + coinType[family], hdkeychain.HardenedKeyStart} {
		key, err = key.Derive(idx)
		if err != nil {
			return nil, err
		}
	}
	return &Account{family: family, net: net, key: key}, nil
}

// AccountFromXPrv restores an account from its serialized private key
// (the hot keystore storage form).
func AccountFromXPrv(family, net, xprv string) (*Account, error) {
	key, err := hdkeychain.NewKeyFromString(xprv)
	if err != nil {
		return nil, fmt.Errorf("xprv: %w", err)
	}
	if !key.IsPrivate() {
		return nil, errors.New("xprv string carries no private material")
	}
	return &Account{family: family, net: net, key: key}, nil
}

// Family is the account's chain family.
func (a *Account) Family() string { return a.family }

// XPub is the watch-only account key the host keeps in Coin config.
func (a *Account) XPub() (string, error) {
	pub, err := a.key.Neuter()
	if err != nil {
		return "", err
	}
	return pub.String(), nil
}

// XPrv serializes the account private key (keystore storage form).
func (a *Account) XPrv() string { return a.key.String() }

// HotAddress is the hot wallet address: change branch m/1/0.
func (a *Account) HotAddress() (string, error) {
	return a.addressOn(1, 0)
}

// AddressAt derives a deposit address on the external branch (m/0/i)
// from the PRIVATE account — used by senders for sweep signing.
func (a *Account) AddressAt(idx uint32) (string, error) {
	return a.addressOn(0, idx)
}

func (a *Account) addressOn(branch, idx uint32) (string, error) {
	child, err := a.derive(branch, idx)
	if err != nil {
		return "", err
	}
	return EncodeAddress(a.family, a.net, child.pub)
}

// KeyAt returns the raw private key bytes at branch/idx.
func (a *Account) KeyAt(branch, idx uint32) ([]byte, error) {
	child, err := a.derive(branch, idx)
	if err != nil {
		return nil, err
	}
	return child.priv, nil
}

type derived struct {
	priv []byte
	pub  []byte // compressed 33 bytes
}

func (a *Account) derive(branch, idx uint32) (derived, error) {
	b, err := a.key.Derive(branch)
	if err != nil {
		return derived{}, err
	}
	c, err := b.Derive(idx)
	if err != nil {
		return derived{}, err
	}
	privKey, err := c.ECPrivKey()
	if err != nil {
		return derived{}, err
	}
	pubKey, err := c.ECPubKey()
	if err != nil {
		return derived{}, err
	}
	return derived{priv: privKey.Serialize(), pub: pubKey.SerializeCompressed()}, nil
}

// EncodeAddress renders a compressed public key as the family's
// address form: bech32 P2WPKH on btc/ltc, keccak-derived hex on evm,
// keccak-derived base58check on tron.
func EncodeAddress(family, net string, compressedPub []byte) (string, error) {
	switch family {
	case BTC, LTC:
		return segwitAddress(family, net, compressedPub)
	case EVM:
		return evmAddress(compressedPub)
	case TRON:
		return TronAddress(compressedPub)
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownFamily, family)
}

func segwitAddress(family, net string, pub []byte) (string, error) {
	hrp, ok := segwitHRP[[2]string{family, net}]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrUnknownFamily, family, net)
	}
	conv, err := bech32.ConvertBits(btcutil.Hash160(pub), 8, 5, true)
	if err != nil {
		return "", err
	}
	// Witness version 0 (P2WPKH) rides as the first 5-bit group.
	return bech32.Encode(hrp, append([]byte{0x00}, conv...))
}

// evmAddress derives the EIP-55 address of a compressed public key.
func evmAddress(compressedPub []byte) (string, error) {
	raw, err := uncompressedPubkey(compressedPub)
	if err != nil {
		return "", err
	}
	return common.BytesToAddress(etcrypto.Keccak256(raw)[12:]).Hex(), nil
}

// TronAddress derives the base58check TRON address (0x41 || keccak12)
// of a compressed public key.
func TronAddress(compressedPub []byte) (string, error) {
	raw, err := uncompressedPubkey(compressedPub)
	if err != nil {
		return "", err
	}
	h := etcrypto.Keccak256(raw)[12:]
	payload := make([]byte, 0, 25)
	payload = append(payload, 0x41)
	payload = append(payload, h...)
	return base58.Encode(sha256d(payload)), nil
}

// uncompressedPubkey converts a compressed secp256k1 public key to the
// 64-byte x||y form EVM/TRON addresses hash.
func uncompressedPubkey(compressed []byte) ([]byte, error) {
	pub, err := etcrypto.DecompressPubkey(compressed)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 64)
	pub.X.FillBytes(out[:32])
	pub.Y.FillBytes(out[32:])
	return out, nil
}

// Watch is the watch-only account key: the host-side xpub deposit
// addresses derive from. It holds no private material.
type Watch struct {
	family string
	net    string
	key    *hdkeychain.ExtendedKey
}

// ImportXPub parses the ceremony-exported account xpub.
func ImportXPub(family, net, xpub string) (*Watch, error) {
	key, err := hdkeychain.NewKeyFromString(xpub)
	if err != nil {
		return nil, fmt.Errorf("xpub: %w", err)
	}
	if key.IsPrivate() {
		return nil, errors.New("xpub carries private material")
	}
	return &Watch{family: family, net: net, key: key}, nil
}

// AddressAt derives deposit address m/0/i (public derivation only).
func (w *Watch) AddressAt(idx uint32) (string, error) {
	branch, err := w.key.Derive(0)
	if err != nil {
		return "", err
	}
	child, err := branch.Derive(idx)
	if err != nil {
		return "", err
	}
	pubKey, err := child.ECPubKey()
	if err != nil {
		return "", err
	}
	return EncodeAddress(w.family, w.net, pubKey.SerializeCompressed())
}

// sha256d appends the base58check checksum (first 4 bytes of SHA-256
// applied twice) — the TRON form.
func sha256d(b []byte) []byte {
	s1 := sha256.Sum256(b)
	s2 := sha256.Sum256(s1[:])
	return append(b, s2[:4]...)
}

// Path renders the derivation path of deposit index i.
func Path(idx uint32) string { return fmt.Sprintf("m/0/%d", idx) }

// HotAddress derives the hot wallet address m/1/0 watch-only (usable
// without the keystore, e.g. for UTXO indexing in watch-only mode).
func (w *Watch) HotAddress() (string, error) {
	branch, err := w.key.Derive(1)
	if err != nil {
		return "", err
	}
	// The hot wallet is the leaf m/1/0, not the change-branch node m/1:
	// Account.HotAddress uses addressOn(1,0), and the watcher's hot
	// watchMap entry must match the same leaf.
	leaf, err := branch.Derive(0)
	if err != nil {
		return "", err
	}
	pub, err := leaf.ECPubKey()
	if err != nil {
		return "", err
	}
	return EncodeAddress(w.family, w.net, pub.SerializeCompressed())
}
