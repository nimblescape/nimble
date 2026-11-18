package nimble

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	file, sums := filepath.Join(dir, "a.tgz"), filepath.Join(dir, "checksums.txt")
	os.WriteFile(file, []byte("hello\n"), 0o644)
	const sum = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03" // sha256 of "hello\n"
	os.WriteFile(sums, []byte("0000  other.tgz\n"+sum+"  a.tgz\n"), 0o644)
	if err := VerifySHA256(file, sums, "a.tgz"); err != nil {
		t.Error(err)
	}
	if VerifySHA256(file, sums, "b.tgz") == nil { // the sum of another file doesn't count
		t.Error("a checksum listed for another name was accepted")
	}
	os.WriteFile(file, []byte("changed\n"), 0o644)
	if VerifySHA256(file, sums, "a.tgz") == nil {
		t.Error("a changed file was accepted")
	}
}
