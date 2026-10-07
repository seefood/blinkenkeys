package hid

/*
#include <locale.h>
#include <stdlib.h>
#include <wchar.h>
*/
import "C"

import "unsafe"

// Go never calls setlocale, so the C library stays in the "C" locale
// whatever LANG/LC_ALL say, and go-hid's wcstombs call panics on any
// enumerated device with a non-ASCII name — not just ours. Switch LC_CTYPE
// to UTF-8 (process-wide; there is no per-call alternative) before the first
// enumeration. Names differ per platform: "UTF-8" on macOS, "C.UTF-8" on
// glibc/musl, "en_US.UTF-8" as a last resort.
func init() {
	for _, name := range []string{"UTF-8", "C.UTF-8", "en_US.UTF-8"} {
		cs := C.CString(name)
		ok := C.setlocale(C.LC_CTYPE, cs) != nil
		C.free(unsafe.Pointer(cs))
		if ok {
			return
		}
	}
}

// wcstombsAcceptsNonASCII reports whether the C library can convert a
// non-ASCII wide character (U+2019, the curly apostrophe in macOS device
// names like "Ira’s Trackpad") to a multibyte string — the conversion go-hid
// performs on every enumerated device's strings.
func wcstombsAcceptsNonASCII() bool {
	ws := [2]C.wchar_t{0x2019, 0}
	var buf [8]C.char
	return C.wcstombs(&buf[0], &ws[0], C.size_t(len(buf))) != ^C.size_t(0)
}
