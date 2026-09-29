package models

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

type OperationPayload struct {
	Version          int64
	Resource         string
	ExpectedRevision int64
	SchemaVersion    int64
	Digest           string
}

func (payload OperationPayload) Valid() bool {
	if payload.Version < 1 || payload.Resource == "" || payload.ExpectedRevision < 1 || payload.SchemaVersion < 1 ||
		len(payload.Digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(payload.Digest)
	return err == nil
}

func (payload OperationPayload) ComputeDigest(rawJSON []byte) string {
	hash := sha256.New()
	var number [8]byte
	for _, value := range []int64{payload.Version, payload.ExpectedRevision, payload.SchemaVersion} {
		binary.BigEndian.PutUint64(number[:], uint64(value))
		_, _ = hash.Write(number[:])
	}
	_, _ = hash.Write([]byte(payload.Resource))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(rawJSON)
	return hex.EncodeToString(hash.Sum(nil))
}
