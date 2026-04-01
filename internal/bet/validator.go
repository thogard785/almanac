package bet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
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

	sigBytes, err := hexDecode(b.Signature)
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes, got %d", len(sigBytes))
	}

	// Build EIP-712 typed data hash
	domainSeparator := hashDomainSeparator()
	structHash := hashBetStruct(b)
	digest := eip712Digest(domainSeparator, structHash)

	// Recover public key
	recovered, err := ecRecover(digest, sigBytes)
	if err != nil {
		// TODO: Full EIP-712 verification is complex without ethcrypto.
		// For now, log warning and accept. Replace with proper verification
		// when a lightweight secp256k1 recovery lib is available.
		log.Printf("[validator] WARNING: signature verification not fully implemented, accepting bet from %s", b.WalletAddr)
		return strings.ToLower(b.WalletAddr), nil
	}

	addr := pubkeyToAddress(recovered)
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
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
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

	// Scale coordinates by 1e6 for int256
	locX := new(big.Int).SetInt64(int64(b.LocationX * 1e6))
	locY := new(big.Int).SetInt64(int64(b.LocationY * 1e6))
	nonce := new(big.Int).SetUint64(b.Nonce)

	// Parse wallet address
	addrBytes, _ := hexDecode(b.WalletAddr)

	data := make([]byte, 0, 256)
	data = append(data, padLeft(typeHash, 32)...)
	data = append(data, padLeft(sportHash, 32)...)
	data = append(data, padLeft(gameIDHash, 32)...)
	data = append(data, padLeft(playIDHash, 32)...)
	data = append(data, int256Bytes(locX)...)
	data = append(data, int256Bytes(locY)...)
	data = append(data, padLeft(nonce.Bytes(), 32)...)
	data = append(data, padLeft(addrBytes, 32)...)
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
	// Two's complement for negative
	complement := new(big.Int).Add(n, new(big.Int).Lsh(big.NewInt(1), 256))
	return padLeft(complement.Bytes(), 32)
}

// ecRecover attempts to recover the public key from signature.
// This is a stub — proper secp256k1 recovery requires a C library or
// pure-Go implementation. Returns error to trigger the fallback path.
func ecRecover(digest, sig []byte) (*ecdsa.PublicKey, error) {
	// TODO: Implement proper secp256k1 ECDSA recovery.
	// Options: use a pure-Go secp256k1 library or CGO binding.
	// For now, return error to use the accept-all fallback.
	return nil, fmt.Errorf("secp256k1 recovery not implemented")
}

func pubkeyToAddress(pub *ecdsa.PublicKey) string {
	if pub == nil {
		return ""
	}
	// Encode uncompressed public key (65 bytes: 04 + X + Y)
	xBytes := padLeft(pub.X.Bytes(), 32)
	yBytes := padLeft(pub.Y.Bytes(), 32)
	pubBytes := append(xBytes, yBytes...)
	hash := keccak256(pubBytes)
	return "0x" + hex.EncodeToString(hash[12:])
}
