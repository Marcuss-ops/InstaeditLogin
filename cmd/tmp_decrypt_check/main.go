package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
)

func main() {
	keyB64 := os.Getenv("KEY")
	cipherHex := os.Getenv("CIPHER")
	if os.Getenv("SELFTEST") == "1" {
		enc, err := crypto.NewEncryptor(1, map[uint32]string{1: keyB64})
		if err != nil {
			fmt.Println("ERR NewEncryptor:", err)
			os.Exit(2)
		}
		ct, err := enc.Encrypt("hello-world-roundtrip")
		if err != nil {
			fmt.Println("ERR Encrypt:", err)
			os.Exit(2)
		}
		pt, err := enc.Decrypt(ct)
		if err != nil {
			fmt.Printf("SELFTEST_FAIL: %v\n", err)
			os.Exit(1)
		}
		if pt != "hello-world-roundtrip" {
			fmt.Printf("SELFTEST_FAIL mismatch\n")
			os.Exit(1)
		}
		fmt.Println("SELFTEST_OK")
		os.Exit(0)
	}
	if keyB64 == "" || cipherHex == "" {
		fmt.Println("ERR: KEY and CIPHER env required")
		os.Exit(2)
	}
	ct, err := hex.DecodeString(cipherHex)
	if err != nil {
		fmt.Println("ERR hex cipher:", err)
		os.Exit(2)
	}
	// Legacy single-key map: id=1
	enc, err := crypto.NewEncryptor(1, map[uint32]string{1: keyB64})
	if err != nil {
		fmt.Println("ERR NewEncryptor:", err)
		os.Exit(2)
	}
	pt, err := enc.Decrypt(ct)
	if err != nil {
		fmt.Printf("DECRYPT_FAIL: %v\n", err)
		os.Exit(1)
	}
	// Never print plaintext. Just report success and a masked fingerprint.
	fmt.Printf("DECRYPT_OK len=%d prefix=%s suffix=%s\n",
		len(pt),
		maskedFirst(pt),
		maskedLast(pt))
}

func maskedFirst(s string) string {
	if len(s) == 0 {
		return "(empty)"
	}
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return s[:1] + strings.Repeat("*", 3)
}

func maskedLast(s string) string {
	if len(s) <= 4 {
		return ""
	}
	return strings.Repeat("*", 3) + s[len(s)-1:]
}
