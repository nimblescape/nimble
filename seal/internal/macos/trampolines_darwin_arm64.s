//go:build darwin

#include "textflag.h"

// The two libSystem functions this package imports. Everything else is found with dlsym
// and called by its address.

TEXT macos_dlopen_trampoline<>(SB),NOSPLIT,$0-0
	JMP	macos_dlopen(SB)
GLOBL	·dlopenABI0(SB), RODATA, $8
DATA	·dlopenABI0(SB)/8, $macos_dlopen_trampoline<>(SB)

TEXT macos_dlsym_trampoline<>(SB),NOSPLIT,$0-0
	JMP	macos_dlsym(SB)
GLOBL	·dlsymABI0(SB), RODATA, $8
DATA	·dlsymABI0(SB)/8, $macos_dlsym_trampoline<>(SB)
