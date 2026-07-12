package lockfile

// This file reproduces the file-path ordering vercel's skills CLI uses before
// hashing a skill folder: JavaScript's default `a.localeCompare(b)`, i.e. ICU
// root-collation. That order is NOT byte order — punctuation sorts before
// digits, digits before letters, letters compare case-insensitively first with
// lowercase winning ties ("skill.md" < "Skill.md" < "SKILL.md", and
// "references/x.md" < "SKILL.md"). Reproducing it exactly is what makes
// ComputeDirHash byte-compatible with vercel's computedHash.
//
// ICU compares the two strings' PRIMARY weights across their full length
// first, and only when those are equal falls back to the TERTIARY (case)
// weights — so a per-character comparison is wrong: "SKILL0" < "skill1" even
// though 's' < 'S' at the case level. collateLess implements that two-level
// scheme for the printable-ASCII range (the alphabet of real skill paths),
// with a stable code-point fallback for anything outside it.

// primaryWeight maps a printable-ASCII rune to its ICU root-collation primary
// weight bucket. The relative order was probed empirically from Node 22's
// default localeCompare:
//
//	space _ - , ; : ! ? . ' " ( ) [ ] { } @ * / \ & # % ` ^ + < = > | ~ $
//	then 0-9, then a-z (case-folded).
var primaryWeight [128]uint16

// asciiPrimaryOrder is the probed primary order of the printable-ASCII
// characters, case pairs collapsed (letters appear once, lowercase).
const asciiPrimaryOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789abcdefghijklmnopqrstuvwxyz"

func init() {
	for i, r := range asciiPrimaryOrder {
		// Weights start at 1 so 0 can mean "unmapped".
		primaryWeight[r] = uint16(i + 1)
		if r >= 'a' && r <= 'z' {
			primaryWeight[r-'a'+'A'] = uint16(i + 1) // case-fold at primary level
		}
	}
}

// runePrimary returns the primary weight of r. Runes outside the probed ASCII
// set (control chars, non-ASCII) sort after everything mapped, ordered by code
// point — a stable, documented fallback rather than a faithful ICU emulation.
func runePrimary(r rune) uint32 {
	if r < 128 {
		if w := primaryWeight[r]; w != 0 {
			return uint32(w)
		}
	}
	return uint32(r) + 1<<16
}

// runeTertiary returns the case weight of r: lowercase (and everything that is
// not an uppercase ASCII letter) is 0, uppercase is 1 — lowercase first.
func runeTertiary(r rune) uint8 {
	if r >= 'A' && r <= 'Z' {
		return 1
	}
	return 0
}

// collateLess reports whether a sorts before b under the two-level collation
// described above. Equal strings are not less.
func collateLess(a, b string) bool {
	// Primary pass: case-folded, full length. A shorter string that is a
	// primary-prefix of the other sorts first.
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		pa, pb := runePrimary(ra[i]), runePrimary(rb[i])
		if pa != pb {
			return pa < pb
		}
	}
	if len(ra) != len(rb) {
		return len(ra) < len(rb)
	}

	// Tertiary pass: first case difference decides; lowercase first.
	for i := 0; i < len(ra); i++ {
		ta, tb := runeTertiary(ra[i]), runeTertiary(rb[i])
		if ta != tb {
			return ta < tb
		}
	}
	return false
}
