package bet

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	eip712DomainTypeHash = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId)"))
	betTypeHash          = crypto.Keccak256Hash([]byte("Bet(address wallet,string game_id,string play_id,int256 x,int256 y,uint256 amount,uint256 nonce)"))
)

func ParseWallet(value string) ([20]byte, error) {
	var out [20]byte
	if !common.IsHexAddress(value) {
		return out, fmt.Errorf("invalid wallet address")
	}
	copy(out[:], common.HexToAddress(value).Bytes())
	return out, nil
}

func WalletHex(wallet [20]byte) string {
	return common.BytesToAddress(wallet[:]).Hex()
}

func ParseSignature(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" {
		return nil, fmt.Errorf("missing signature")
	}
	sig, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding")
	}
	if len(sig) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes")
	}
	sig = append([]byte(nil), sig...)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	if sig[64] > 1 {
		return nil, fmt.Errorf("invalid signature recovery id")
	}
	return sig, nil
}

func VerifySignature(b *Bet) error {
	if b == nil {
		return fmt.Errorf("missing bet")
	}
	digest := eip712Digest(domainSeparator(), betStructHash(b))
	pubKey, err := crypto.SigToPub(digest.Bytes(), b.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature")
	}
	recovered := crypto.PubkeyToAddress(*pubKey)
	expected := common.BytesToAddress(b.Wallet[:])
	if recovered != expected {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func domainSeparator() common.Hash {
	return crypto.Keccak256Hash(
		eip712DomainTypeHash.Bytes(),
		crypto.Keccak256([]byte(DomainName)),
		crypto.Keccak256([]byte(DomainVersion)),
		common.BigToHash(new(big.Int).SetUint64(DomainChainID)).Bytes(),
	)
}

func betStructHash(b *Bet) common.Hash {
	return crypto.Keccak256Hash(
		betTypeHash.Bytes(),
		common.LeftPadBytes(b.Wallet[:], 32),
		crypto.Keccak256([]byte(b.GameID)),
		crypto.Keccak256([]byte(b.PlayID)),
		int256Bytes(truncateScaled(b.Coordinate.X, 1000)),
		int256Bytes(truncateScaled(b.Coordinate.Y, 1000)),
		uint256Bytes(uint64(math.Trunc(b.Amount*100))),
		uint256Bytes(b.Nonce),
	)
}

func eip712Digest(domainHash, structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, domainHash.Bytes(), structHash.Bytes())
}

func truncateScaled(v float64, scale float64) *big.Int {
	return big.NewInt(int64(math.Trunc(v * scale)))
}

func uint256Bytes(v uint64) []byte {
	return common.BigToHash(new(big.Int).SetUint64(v)).Bytes()
}

func int256Bytes(v *big.Int) []byte {
	if v.Sign() >= 0 {
		return common.BigToHash(v).Bytes()
	}
	mod := new(big.Int).Lsh(big.NewInt(1), 256)
	return common.BigToHash(new(big.Int).Add(v, mod)).Bytes()
}
