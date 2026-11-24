//go:build darwin

// Package macos calls the Security and CoreFoundation frameworks of macOS without cgo, so
// that nimble keeps building with CGO_ENABLED=0. It imports two functions from libSystem,
// dlopen and dlsym, and calls them through the libc call path of the Go runtime, as
// golang.org/x/sys/unix does. Every other function and constant it finds with dlsym and
// calls by its address on the same path.
//
// It does one job: it finds the private keys that macOS keeps on tokens (a key in the
// Secure Enclave made by sc_auth create-ctk-identity, a smart card) and runs an ECDH key
// agreement with one of them. The key never leaves the token: the token computes the
// shared secret, after Touch ID when the key asks for it.
package macos

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

//go:cgo_import_dynamic macos_dlopen dlopen "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic macos_dlsym dlsym "/usr/lib/libSystem.B.dylib"

// The addresses of the trampolines in the assembly file.
var dlopenABI0, dlsymABI0 uintptr

// syscall6 is the libc call path of the runtime: it switches to the system stack, calls fn
// with the C calling convention and tells the scheduler that the goroutine is blocked (the
// call may wait for Touch ID).
//
//go:linkname syscall6 syscall.syscall6
func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr)

// call calls the C function at fn with up to six integer or pointer arguments. Extra
// arguments are zero, which C ignores.
func call(fn uintptr, args ...uintptr) uintptr {
	var a [6]uintptr
	copy(a[:], args)
	r, _, _ := syscall6(fn, a[0], a[1], a[2], a[3], a[4], a[5])
	return r
}

const rtldNow = 0x2

var (
	loadOnce sync.Once
	loadErr  error
	fn       struct {
		// CoreFoundation
		CFRelease, CFDataCreate, CFDataGetLength, CFDataGetBytePtr, CFStringCreateWithBytes,
		CFStringGetLength, CFStringGetCString, CFNumberCreate, CFDictionaryCreate,
		CFDictionaryGetValue, CFArrayGetCount, CFArrayGetValueAtIndex, CFGetTypeID,
		CFArrayGetTypeID, CFDictionaryGetTypeID, CFDataGetTypeID, CFStringGetTypeID,
		CFErrorCopyDescription, CFErrorGetCode uintptr
		// Security
		SecItemCopyMatching, SecKeyCopyPublicKey, SecKeyCopyExternalRepresentation,
		SecKeyIsAlgorithmSupported, SecKeyCreateWithData, SecKeyCopyKeyExchangeResult uintptr
	}
	// Constants: the values of the framework's global variables (CFStringRef and others).
	k struct {
		CFBooleanTrue, Class, ClassKey, AttrKeyClass, AttrKeyClassPrivate, AttrKeyClassPublic,
		AttrKeyType, AttrKeyTypeECSECPrimeRandom, AttrKeySizeInBits, AttrAccessGroup,
		AttrAccessGroupToken, AttrLabel, AttrApplicationLabel, AttrTokenID, MatchLimit,
		MatchLimitAll, MatchLimitOne, ReturnRef, ReturnAttributes, ValueRef,
		UseOperationPrompt, ECDHStandard uintptr
		// addresses of structs, not their values
		DictKeyCallBacks, DictValueCallBacks uintptr
	}
)

// cstring is a NUL-terminated copy of s, kept alive by the caller.
func cstring(s string) []byte { return append([]byte(s), 0) }

func dlopen(path string) uintptr {
	p := cstring(path)
	h := call(dlopenABI0, uintptr(unsafe.Pointer(&p[0])), rtldNow)
	runtime.KeepAlive(p)
	return h
}

func dlsym(lib uintptr, name string) uintptr {
	p := cstring(name)
	a := call(dlsymABI0, lib, uintptr(unsafe.Pointer(&p[0])))
	runtime.KeepAlive(p)
	return a
}

// ptr turns an address from C into a pointer without a uintptr conversion that go vet
// rejects: the address is C memory, which the Go collector neither moves nor frees.
func ptr(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

// load finds the functions and constants once.
func load() error {
	loadOnce.Do(func() {
		cf := dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		sec := dlopen("/System/Library/Frameworks/Security.framework/Security")
		if cf == 0 || sec == 0 {
			loadErr = errors.New("the Security framework of macOS can't be loaded")
			return
		}
		var missing []string
		funcs := func(lib uintptr, names map[string]*uintptr) {
			for name, p := range names {
				if *p = dlsym(lib, name); *p == 0 {
					missing = append(missing, name)
				}
			}
		}
		// value reads a global variable (a CFStringRef and the like); optional ones may be 0
		value := func(lib uintptr, name string, p *uintptr, optional bool) {
			if a := dlsym(lib, name); a != 0 {
				*p = *(*uintptr)(ptr(a))
			}
			if *p == 0 && !optional {
				missing = append(missing, name)
			}
		}
		funcs(cf, map[string]*uintptr{
			"CFRelease": &fn.CFRelease, "CFDataCreate": &fn.CFDataCreate, "CFDataGetLength": &fn.CFDataGetLength,
			"CFDataGetBytePtr": &fn.CFDataGetBytePtr, "CFStringCreateWithBytes": &fn.CFStringCreateWithBytes,
			"CFStringGetLength": &fn.CFStringGetLength, "CFStringGetCString": &fn.CFStringGetCString,
			"CFNumberCreate": &fn.CFNumberCreate, "CFDictionaryCreate": &fn.CFDictionaryCreate,
			"CFDictionaryGetValue": &fn.CFDictionaryGetValue, "CFArrayGetCount": &fn.CFArrayGetCount,
			"CFArrayGetValueAtIndex": &fn.CFArrayGetValueAtIndex, "CFGetTypeID": &fn.CFGetTypeID,
			"CFArrayGetTypeID": &fn.CFArrayGetTypeID, "CFDictionaryGetTypeID": &fn.CFDictionaryGetTypeID,
			"CFDataGetTypeID": &fn.CFDataGetTypeID, "CFStringGetTypeID": &fn.CFStringGetTypeID,
			"CFErrorCopyDescription": &fn.CFErrorCopyDescription, "CFErrorGetCode": &fn.CFErrorGetCode,
		})
		funcs(sec, map[string]*uintptr{
			"SecItemCopyMatching": &fn.SecItemCopyMatching, "SecKeyCopyPublicKey": &fn.SecKeyCopyPublicKey,
			"SecKeyCopyExternalRepresentation": &fn.SecKeyCopyExternalRepresentation,
			"SecKeyIsAlgorithmSupported":       &fn.SecKeyIsAlgorithmSupported,
			"SecKeyCreateWithData":             &fn.SecKeyCreateWithData,
			"SecKeyCopyKeyExchangeResult":      &fn.SecKeyCopyKeyExchangeResult,
		})
		value(cf, "kCFBooleanTrue", &k.CFBooleanTrue, false)
		for name, p := range map[string]*uintptr{
			"kSecClass": &k.Class, "kSecClassKey": &k.ClassKey, "kSecAttrKeyClass": &k.AttrKeyClass,
			"kSecAttrKeyClassPrivate": &k.AttrKeyClassPrivate, "kSecAttrKeyClassPublic": &k.AttrKeyClassPublic,
			"kSecAttrKeyType": &k.AttrKeyType, "kSecAttrKeyTypeECSECPrimeRandom": &k.AttrKeyTypeECSECPrimeRandom,
			"kSecAttrKeySizeInBits": &k.AttrKeySizeInBits, "kSecAttrAccessGroup": &k.AttrAccessGroup,
			"kSecAttrAccessGroupToken": &k.AttrAccessGroupToken, "kSecAttrLabel": &k.AttrLabel,
			"kSecAttrApplicationLabel": &k.AttrApplicationLabel, "kSecAttrTokenID": &k.AttrTokenID,
			"kSecMatchLimit": &k.MatchLimit, "kSecMatchLimitAll": &k.MatchLimitAll, "kSecMatchLimitOne": &k.MatchLimitOne,
			"kSecReturnRef": &k.ReturnRef, "kSecReturnAttributes": &k.ReturnAttributes, "kSecValueRef": &k.ValueRef,
			"kSecKeyAlgorithmECDHKeyExchangeStandard": &k.ECDHStandard,
		} {
			value(sec, name, p, false)
		}
		value(sec, "kSecUseOperationPrompt", &k.UseOperationPrompt, true) // deprecated: only the text of the prompt
		if k.DictKeyCallBacks = dlsym(cf, "kCFTypeDictionaryKeyCallBacks"); k.DictKeyCallBacks == 0 {
			missing = append(missing, "kCFTypeDictionaryKeyCallBacks")
		}
		if k.DictValueCallBacks = dlsym(cf, "kCFTypeDictionaryValueCallBacks"); k.DictValueCallBacks == 0 {
			missing = append(missing, "kCFTypeDictionaryValueCallBacks")
		}
		if len(missing) > 0 {
			loadErr = fmt.Errorf("the Security framework of this macOS lacks %v", missing)
		}
	})
	return loadErr
}

// CoreFoundation helpers. A function named Create or Copy returns an object the caller
// releases.

const (
	utf8Encoding    = 0x08000100 // kCFStringEncodingUTF8
	sint32Type      = 3          // kCFNumberSInt32Type
	errItemNotFound = -25300     // errSecItemNotFound
	opKeyExchange   = 4          // kSecKeyOperationTypeKeyExchange
)

func release(obj uintptr) {
	if obj != 0 {
		call(fn.CFRelease, obj)
	}
}

func cfString(s string) uintptr {
	b := []byte(s)
	if len(b) == 0 {
		b = []byte{0}
	}
	r := call(fn.CFStringCreateWithBytes, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(s)), utf8Encoding, 0)
	runtime.KeepAlive(b)
	return r
}

func cfData(b []byte) uintptr {
	if len(b) == 0 {
		return call(fn.CFDataCreate, 0, 0, 0)
	}
	r := call(fn.CFDataCreate, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	runtime.KeepAlive(b)
	return r
}

func cfNumber(v int32) uintptr {
	p := new(int32)
	*p = v
	r := call(fn.CFNumberCreate, 0, sint32Type, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
	return r
}

// cfDict builds a dictionary from key-value pairs (CF objects, retained by the dictionary).
func cfDict(pairs ...uintptr) uintptr {
	n := len(pairs) / 2
	keys, values := make([]uintptr, n+1), make([]uintptr, n+1) // +1: never empty
	for i := 0; i < n; i++ {
		keys[i], values[i] = pairs[2*i], pairs[2*i+1]
	}
	r := call(fn.CFDictionaryCreate, 0, uintptr(unsafe.Pointer(&keys[0])), uintptr(unsafe.Pointer(&values[0])),
		uintptr(n), k.DictKeyCallBacks, k.DictValueCallBacks)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(values)
	return r
}

func typeIs(obj, typeIDFunc uintptr) bool {
	return obj != 0 && call(fn.CFGetTypeID, obj) == call(typeIDFunc)
}

func goBytes(data uintptr) []byte {
	if !typeIs(data, fn.CFDataGetTypeID) {
		return nil
	}
	n := int(call(fn.CFDataGetLength, data))
	p := call(fn.CFDataGetBytePtr, data)
	if n <= 0 || p == 0 {
		return nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(ptr(p)), n)...)
}

func goString(s uintptr) string {
	if !typeIs(s, fn.CFStringGetTypeID) {
		return ""
	}
	n := int(call(fn.CFStringGetLength, s))*4 + 1 // UTF-16 units: at most 4 bytes each in UTF-8
	buf := make([]byte, n)
	ok := call(fn.CFStringGetCString, s, uintptr(unsafe.Pointer(&buf[0])), uintptr(n), utf8Encoding) & 0xff
	runtime.KeepAlive(buf)
	if ok == 0 {
		return ""
	}
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// cfError turns a CFErrorRef (released here) into an error.
func cfError(what string, e uintptr) error {
	if e == 0 {
		return errors.New(what + " failed")
	}
	defer release(e)
	desc := call(fn.CFErrorCopyDescription, e)
	defer release(desc)
	return fmt.Errorf("%s: %s (%d)", what, goString(desc), int(call(fn.CFErrorGetCode, e)))
}

// Key is a private key on a token.
type Key struct {
	Label       string // kSecAttrLabel
	Hash        []byte // kSecAttrApplicationLabel: SHA-1 of the public key, as sc_auth lists it
	Token       string // kSecAttrTokenID, e.g. com.apple.setoken for the Secure Enclave
	Public      []byte // the public key; for P-256 the uncompressed point (65 bytes)
	KeyExchange bool   // the token runs an ECDH key agreement with it
}

// TokenKeys lists the private keys that macOS keeps on tokens: Secure Enclave identities
// and smart cards. It needs no Touch ID.
func TokenKeys() ([]Key, error) {
	if err := load(); err != nil {
		return nil, err
	}
	query := cfDict(
		k.Class, k.ClassKey,
		k.AttrKeyClass, k.AttrKeyClassPrivate,
		k.AttrAccessGroup, k.AttrAccessGroupToken,
		k.MatchLimit, k.MatchLimitAll,
		k.ReturnRef, k.CFBooleanTrue,
		k.ReturnAttributes, k.CFBooleanTrue,
	)
	defer release(query)
	var result uintptr
	status := int32(call(fn.SecItemCopyMatching, query, uintptr(unsafe.Pointer(&result))))
	if status == errItemNotFound {
		return nil, nil
	}
	if status != 0 {
		return nil, fmt.Errorf("listing the token keys (SecItemCopyMatching): OSStatus %d", status)
	}
	defer release(result)
	var items []uintptr
	switch {
	case typeIs(result, fn.CFArrayGetTypeID):
		for i, n := uintptr(0), call(fn.CFArrayGetCount, result); i < n; i++ {
			items = append(items, call(fn.CFArrayGetValueAtIndex, result, i))
		}
	case typeIs(result, fn.CFDictionaryGetTypeID):
		items = append(items, result)
	}
	var keys []Key
	for _, item := range items {
		if !typeIs(item, fn.CFDictionaryGetTypeID) {
			continue
		}
		get := func(key uintptr) uintptr { return call(fn.CFDictionaryGetValue, item, key) }
		key := Key{
			Label: goString(get(k.AttrLabel)),
			Hash:  goBytes(get(k.AttrApplicationLabel)),
			Token: goString(get(k.AttrTokenID)),
		}
		if ref := get(k.ValueRef); ref != 0 { // owned by the dictionary
			key.Public = publicKey(ref)
			key.KeyExchange = call(fn.SecKeyIsAlgorithmSupported, ref, opKeyExchange, k.ECDHStandard)&0xff != 0
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// publicKey is the external representation of the public key of a private key ref.
func publicKey(priv uintptr) []byte {
	pub := call(fn.SecKeyCopyPublicKey, priv)
	if pub == 0 {
		return nil
	}
	defer release(pub)
	var cfErr uintptr
	data := call(fn.SecKeyCopyExternalRepresentation, pub, uintptr(unsafe.Pointer(&cfErr)))
	if data == 0 {
		release(cfErr)
		return nil
	}
	defer release(data)
	return goBytes(data)
}

// ECDH runs the key agreement of the token key whose public key hash is hash with the P-256
// public key peer (an uncompressed point, 65 bytes) and returns the shared secret: the x
// coordinate of the shared point, 32 bytes, as crypto/ecdh returns it. The token may ask
// for Touch ID or a PIN first; prompt is the text macOS shows with it, where it shows one.
func ECDH(hash, peer []byte, prompt string) ([]byte, error) {
	if err := load(); err != nil {
		return nil, err
	}
	hashData := cfData(hash)
	defer release(hashData)
	pairs := []uintptr{
		k.Class, k.ClassKey,
		k.AttrKeyClass, k.AttrKeyClassPrivate,
		k.AttrAccessGroup, k.AttrAccessGroupToken,
		k.AttrApplicationLabel, hashData,
		k.MatchLimit, k.MatchLimitOne,
		k.ReturnRef, k.CFBooleanTrue,
	}
	if prompt != "" && k.UseOperationPrompt != 0 {
		p := cfString(prompt)
		defer release(p)
		pairs = append(pairs, k.UseOperationPrompt, p)
	}
	query := cfDict(pairs...)
	defer release(query)
	var priv uintptr
	status := int32(call(fn.SecItemCopyMatching, query, uintptr(unsafe.Pointer(&priv))))
	if status == errItemNotFound {
		return nil, ErrNoKey
	}
	if status != 0 {
		return nil, fmt.Errorf("finding the token key (SecItemCopyMatching): OSStatus %d", status)
	}
	defer release(priv)

	peerData := cfData(peer)
	defer release(peerData)
	size := cfNumber(256)
	defer release(size)
	attrs := cfDict(
		k.AttrKeyType, k.AttrKeyTypeECSECPrimeRandom,
		k.AttrKeyClass, k.AttrKeyClassPublic,
		k.AttrKeySizeInBits, size,
	)
	defer release(attrs)
	var cfErr uintptr
	pub := call(fn.SecKeyCreateWithData, peerData, attrs, uintptr(unsafe.Pointer(&cfErr)))
	if pub == 0 {
		return nil, cfError("reading the peer public key (SecKeyCreateWithData)", cfErr)
	}
	defer release(pub)

	params := cfDict()
	defer release(params)
	cfErr = 0
	secret := call(fn.SecKeyCopyKeyExchangeResult, priv, k.ECDHStandard, pub, params, uintptr(unsafe.Pointer(&cfErr)))
	if secret == 0 {
		return nil, cfError("key agreement on the token (SecKeyCopyKeyExchangeResult)", cfErr)
	}
	defer release(secret)
	out := goBytes(secret)
	if len(out) != 32 {
		return nil, fmt.Errorf("key agreement on the token returned %d bytes, not 32", len(out))
	}
	return out, nil
}

// Supported reports whether this build can reach the tokens of macOS.
const Supported = true
