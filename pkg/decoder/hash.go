package decoder

import (
	"hash"

	"github.com/pierrec/xxHash/xxHash64"
	"golang.org/x/crypto/blake2b"
)

func TwoxHash(data []byte, bitLen int) []byte {
	var (
		byteLen    = int(bitLen / 8)
		iterations = int(bitLen / 64)
	)
	hash := make([]byte, byteLen)
	for seed := 0; seed < iterations; seed++ {
		digest := xxHash64.New(uint64(seed))
		digest.Write(data)
		copy(hash[seed*8:], digest.Sum(nil))
	}
	return hash
}

func Blake2Hash(data []byte, bitLen int) []byte {
	var checksum hash.Hash
	switch bitLen {
	case 256:
		checksum, _ = blake2b.New256([]byte{})
	default:
		checksum, _ = blake2b.New(16, []byte{})
	}
	checksum.Write(data)
	return checksum.Sum(nil)
}

func DoHash(data []byte, hasher string) []byte {
	switch hasher {
	case "Blake2_128":
		return Blake2Hash(data, 128)
	case "Blake2_256":
		return Blake2Hash(data, 256)
	case "Twox128":
		return TwoxHash(data, 128)
	case "Twox256":
		return TwoxHash(data, 256)
	case "Identity":
		return data[:]
	case "Blake2_128Concat":
		return append(Blake2Hash(data, 128), data...)
	default:
		return append(TwoxHash(data, 64), data...)
	}
}
