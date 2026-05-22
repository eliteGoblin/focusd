// focusd-sign appends an Ed25519 signature trailer to a built binary.
// Release/build step only — the single place the private key is used.
//
//	focusd-sign -in <binary> -out <signed> [-key <pem>]
//
// Key source: -key path, else $FOCUSD_ED25519_PRIVATE_KEY (PEM contents,
// used by CI from the GH secret), else ~/.creds/focusd_ed25519_private.pem.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eliteGoblin/focusd/daemon/internal/sig"
)

func main() {
	in := flag.String("in", "", "input binary")
	out := flag.String("out", "", "output signed binary")
	key := flag.String("key", "", "Ed25519 PKCS8 PEM private key path")
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: focusd-sign -in <bin> -out <signed> [-key pem]")
		os.Exit(2)
	}
	pemBytes, err := loadKey(*key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "key error:", err)
		os.Exit(2)
	}
	if err := sig.SignFile(*in, *out, pemBytes); err != nil {
		fmt.Fprintln(os.Stderr, "sign error:", err)
		os.Exit(1)
	}
	fmt.Printf("signed %s -> %s\n", *in, *out)
}

func loadKey(path string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	if env := os.Getenv("FOCUSD_ED25519_PRIVATE_KEY"); env != "" {
		return []byte(env), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(home, ".creds", "focusd_ed25519_private.pem"))
}
