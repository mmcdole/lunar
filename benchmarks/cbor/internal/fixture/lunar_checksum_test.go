//go:build !gopherlua_reference

package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSmallPresetHasPublishedLunarChecksum(t *testing.T) {
	preset, ok := Lookup("small")
	if !ok {
		t.Fatal("small preset is missing")
	}
	path := filepath.Join(t.TempDir(), "small.cbor")
	if _, err := WriteCBOR(fixturePath(t), path, preset); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	const wantSHA256 = "5a840e6955b60c49832742a9e279c0d92163abceb96a22d79e7ce22c98d4b633"
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		t.Fatalf("small preset SHA-256 = %s, want %s", got, wantSHA256)
	}
}
