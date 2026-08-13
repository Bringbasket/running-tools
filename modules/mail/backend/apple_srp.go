package mail

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"math/big"
)

var (
	appleSRPN = mustParseAppleBigHex(
		"AC6BDB41324A9A9BF166DE5E1389582FAF72B6651987EE07FC3192943DB56050" +
			"A37329CBB4A099ED8193E0757767A13DD52312AB4B03310DCD7F48A9DA04FD50" +
			"E8083969EDB767B0CF6095179A163AB3661A05FBD5FAAAE82918A9962F0B93B8" +
			"55F97993EC975EEAA80D740ADBF4FF747359D041D5C33EA71D281E446B14773B" +
			"CA97B43A23FB801676BD207A436C6481F1D2B9078717461A5B9D32E688F87748" +
			"544523B524B0D57D5EA77A2775D2ECFA032CFBDBF52FB3786160279004E57AE" +
			"6AF874E7303CE53299CCC041C7BC308D82A5698F3A8D0C38271AE35F8E9DBFB" +
			"B694B5C803D89F7AE435DE236D525F54759B65E372FCD68EF20FA7111F9E4AFF73")
	appleSRPG = big.NewInt(2)
)

type appleSRPClient struct {
	a  *big.Int
	A  *big.Int
	k  *big.Int
	M1 []byte
	M2 []byte
}

func newAppleSRPClient() (*appleSRPClient, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("srp random: %w", err)
	}
	a := new(big.Int).SetBytes(secret)
	return &appleSRPClient{a: a, A: new(big.Int).Exp(appleSRPG, a, appleSRPN), k: appleSRPMultiplier()}, nil
}

func (client *appleSRPClient) ABytes() []byte { return appleSRPPad(client.A) }

func (client *appleSRPClient) processChallenge(username, derivedKey, salt, serverB []byte) error {
	server := new(big.Int).SetBytes(serverB)
	if server.Sign() <= 0 || server.Cmp(appleSRPN) >= 0 {
		return fmt.Errorf("srp invalid server B")
	}
	x := appleSRPX(salt, derivedKey)
	u := appleSRPU(client.A, server)
	if u.Sign() == 0 {
		return fmt.Errorf("srp invalid u")
	}
	shared := appleSRPS(client.k, x, client.a, server, u)
	key := appleSRPHash(shared)
	aBytes, bBytes := appleSRPPad(client.A), appleSRPPad(server)
	client.M1 = appleSRPM1(username, salt, aBytes, bBytes, key)
	client.M2 = appleSRPM2(aBytes, client.M1, key)
	return nil
}

func deriveAppleSRPPassword(password string, salt []byte, iterations int, protocol string) ([]byte, error) {
	hashed := sha256.Sum256([]byte(password))
	var input string
	switch protocol {
	case "s2k":
		input = string(hashed[:])
	case "s2k_fo":
		input = hex.EncodeToString(hashed[:])
	default:
		return nil, fmt.Errorf("unsupported SRP protocol %q", protocol)
	}
	return pbkdf2.Key(sha256.New, input, salt, iterations, 32)
}

func mustParseAppleBigHex(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 16)
	if !ok {
		panic("bad SRP constant")
	}
	return result
}

func appleSRPPad(value *big.Int) []byte {
	data := value.Bytes()
	if len(data) >= 256 {
		return data
	}
	result := make([]byte, 256)
	copy(result[256-len(data):], data)
	return result
}

func appleSRPHash(data []byte) []byte {
	hasher := sha256.New()
	_, _ = hasher.Write(data)
	return hasher.Sum(nil)
}

func appleSRPHashInt(hasher hash.Hash) *big.Int { return new(big.Int).SetBytes(hasher.Sum(nil)) }

func appleSRPMultiplier() *big.Int {
	hasher := sha256.New()
	nBytes, gBytes := appleSRPN.Bytes(), appleSRPG.Bytes()
	for len(gBytes) < len(nBytes) {
		gBytes = append([]byte{0}, gBytes...)
	}
	_, _ = hasher.Write(nBytes)
	_, _ = hasher.Write(gBytes)
	return appleSRPHashInt(hasher)
}

func appleSRPX(salt, derived []byte) *big.Int {
	inner := sha256.New()
	_, _ = inner.Write([]byte(":"))
	_, _ = inner.Write(derived)
	outer := sha256.New()
	_, _ = outer.Write(salt)
	_, _ = outer.Write(inner.Sum(nil))
	return new(big.Int).SetBytes(outer.Sum(nil))
}

func appleSRPU(a, b *big.Int) *big.Int {
	hasher := sha256.New()
	_, _ = hasher.Write(appleSRPPad(a))
	_, _ = hasher.Write(appleSRPPad(b))
	return appleSRPHashInt(hasher)
}

func appleSRPS(k, x, a, b, u *big.Int) []byte {
	gx := new(big.Int).Exp(appleSRPG, x, appleSRPN)
	difference := new(big.Int).Sub(b, new(big.Int).Mul(k, gx))
	exponent := new(big.Int).Add(a, new(big.Int).Mul(u, x))
	shared := new(big.Int).Exp(difference, exponent, appleSRPN)
	shared.Mod(shared, appleSRPN)
	return appleSRPPad(shared)
}

func appleSRPM1(username, salt, a, b, key []byte) []byte {
	hg, hn := appleSRPHash(appleSRPPad(appleSRPG)), appleSRPHash(appleSRPN.Bytes())
	xor := make([]byte, len(hg))
	for index := range hg {
		xor[index] = hg[index] ^ hn[index]
	}
	hasher := sha256.New()
	_, _ = hasher.Write(xor)
	_, _ = hasher.Write(appleSRPHash(username))
	_, _ = hasher.Write(salt)
	_, _ = hasher.Write(a)
	_, _ = hasher.Write(b)
	_, _ = hasher.Write(key)
	return hasher.Sum(nil)
}

func appleSRPM2(a, m1, key []byte) []byte {
	hasher := sha256.New()
	_, _ = hasher.Write(a)
	_, _ = hasher.Write(m1)
	_, _ = hasher.Write(key)
	return hasher.Sum(nil)
}
