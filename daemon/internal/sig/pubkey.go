package sig

// pubkey.go: runtime XOR-decoder for the embedded Ed25519 public key.
//
// The decoded PEM is the same bytes that previously lived in a `//go:embed
// focusd_ed25519_public.pem` variable. Storing the bytes XOR-masked
// means `strings <daemon> | grep "BEGIN PUBLIC KEY"` no longer locates
// the key inside the binary.
//
// HONEST SCOPE — this is friction, not protection:
//   - The mask is deterministic and the label that derives it lives in
//     this same binary. An attacker who loads the daemon in a
//     disassembler (Ghidra, Hopper, IDA) can identify loadPublicKeyPEM,
//     follow the call into deriveMask + xorMask, and recover the PEM in
//     minutes. A memory dump after first verification also leaks it.
//   - The single goal is to defeat the lowest-effort grep/sed attack
//     against the binary, and to slow down naive LLM-assisted extraction.
//
// The label is intentionally split across two string literals so a single
// `strings | grep "focusd-pubkey-mask-v1"` against the binary doesn't
// reveal it either.

import "crypto/sha256"

// loadPublicKeyPEM XOR-decodes maskedPubkey using the deterministic mask
// and returns the original PEM bytes.
func loadPublicKeyPEM() []byte {
	mask := deriveMask()
	out := make([]byte, len(maskedPubkey))
	for i, b := range maskedPubkey {
		out[i] = b ^ mask[i%len(mask)]
	}
	return out
}

// deriveMask reconstructs the 32-byte XOR mask without ever forming the
// full label as a compile-time constant. The two halves are kept in
// separate `var` strings and joined into a byte slice at runtime so the
// Go compiler can't fold them into a contiguous "focusd-pubkey-mask-v1"
// literal that `strings | grep` would find in the binary.
func deriveMask() [32]byte {
	// Intentionally var (not const) so the compiler does not constant-fold
	// the concatenation. Building the input via append on a byte slice
	// keeps the two halves separated in the binary.
	var labelA = "focusd-pubkey-"
	var labelB = "mask-v1"
	buf := make([]byte, 0, len(labelA)+len(labelB))
	buf = append(buf, labelA...)
	buf = append(buf, labelB...)
	return sha256.Sum256(buf)
}
