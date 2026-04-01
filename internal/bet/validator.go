package bet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ValidateSignature verifies the EIP-712 signature on a bet.
// Returns the recovered address (lowercase, 0x-prefixed) or error.
//
// EIP-712 domain: { name: "Almanac", version: "1", chainId: 10143 }
// Types: Bet: [sport(string), gameId(string), playId(string),
//
//	locationX(int256 scaled 1e6), locationY(int256 scaled 1e6),
//	nonce(uint256), walletAddr(address)]
func ValidateSignature(b *Bet) (string, error) {
	if b.Signature == "" || b.WalletAddr == "" {
		return "", fmt.Errorf("missing signature or wallet address")
	}
	if !common.IsHexAddress(b.WalletAddr) {
		return "", fmt.Errorf("invalid wallet address: %s", b.WalletAddr)
	}

	sigBytes, err := hexDecode(b.Signature)
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes, got %d", len(sigBytes))
	}

	// Normalize recovery ID for go-ethereum: 0/1 instead of 27/28.
	sigBytes = append([]byte(nil), sigBytes...)
	if sigBytes[64] >= 27 {
		sigBytes[64] -= 27
	}
	if sigBytes[64] > 1 {
		return "", fmt.Errorf("invalid recovery id %d", sigBytes[64])
	}

	// Build EIP-712 typed data hash.
	domainSeparator := hashDomainSeparator()
	structHash := hashBetStruct(b)
	digest := eip712Digest(domainSeparator, structHash)

	pub, err := ecRecover(digest, sigBytes)
	if err != nil {
		return "", fmt.Errorf("recover signer: %w", err)
	}

	addr := ethcrypto.PubkeyToAddress(*pub).Hex()
	if !strings.EqualFold(addr, b.WalletAddr) {
		return "", fmt.Errorf("recovered address %s does not match wallet %s", addr, b.WalletAddr)
	}
	return strings.ToLower(addr), nil
}

func hexDecode(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	return hex.DecodeString(s)
}

func keccak256(data []byte) []byte {
	return ethcrypto.Keccak256(data)
}

func hashDomainSeparator() []byte {
	// EIP712Domain(string name,string version,uint256 chainId)
	typeHash := keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId)"))
	nameHash := keccak256([]byte(EIP712Domain))
	versionHash := keccak256([]byte(EIP712Version))
	chainID := big.NewInt(EIP712ChainID)

	// ABI encode: typeHash + nameHash + versionHash + chainId
	data := make([]byte, 0, 128)
	data = append(data, padLeft(typeHash, 32)...)
	data = append(data, padLeft(nameHash, 32)...)
	data = append(data, padLeft(versionHash, 32)...)
	data = append(data, padLeft(chainID.Bytes(), 32)...)
	return keccak256(data)
}

func hashBetStruct(b *Bet) []byte {
	// Bet(string sport,string gameId,string playId,int256 locationX,int256 locationY,uint256 nonce,address walletAddr)
	typeHash := keccak256([]byte("Bet(string sport,string gameId,string playId,int256 locationX,int256 locationY,uint256 nonce,address walletAddr)"))

	sportHash := keccak256([]byte(b.Sport))
	gameIDHash := keccak256([]byte(b.GameID))
	playIDHash := keccak256([]byte(b.PlayID))

	// Scale coordinates by 1e6 for int256.
	locX := new(big.Int).SetInt64(int64(b.LocationX * 1e6))
	locY := new(big.Int).SetInt64(int64(b.LocationY * 1e6))
	nonce := new(big.Int).SetUint64(b.Nonce)

	addr := common.HexToAddress(b.WalletAddr)

	data := make([]byte, 0, 256)
	data = append(data, padLeft(typeHash, 32)...)
	data = append(data, padLeft(sportHash, 32)...)
	data = append(data, padLeft(gameIDHash, 32)...)
	data = append(data, padLeft(playIDHash, 32)...)
	data = append(data, int256Bytes(locX)...)
	data = append(data, int256Bytes(locY)...)
	data = append(data, padLeft(nonce.Bytes(), 32)...)
	data = append(data, padLeft(addr.Bytes(), 32)...)
	return keccak256(data)
}

func eip712Digest(domainSeparator, structHash []byte) []byte {
	// "\x19\x01" + domainSeparator + structHash
	data := make([]byte, 0, 66)
	data = append(data, 0x19, 0x01)
	data = append(data, domainSeparator...)
	data = append(data, structHash...)
	return keccak256(data)
}

func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b[:size]
	}
	padded := make([]byte, size)
	copy(padded[size-len(b):], b)
	return padded
}

func int256Bytes(n *big.Int) []byte {
	if n.Sign() >= 0 {
		return padLeft(n.Bytes(), 32)
	}
	// Two's complement for negative.
	complement := new(big.Int).Add(n, new(big.Int).Lsh(big.NewInt(1), 256))
	return padLeft(complement.Bytes(), 32)
}

func ecRecover(digest, sig []byte) (*ecdsa.PublicKey, error) {
	return ethcrypto.SigToPub(digest, sig)
}
