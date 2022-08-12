package zerocopy

import (
	"reflect"
	"unsafe"
)

// zero-copy slice convert to string
func String(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// zero-copy slice convert to string
func Bytes(s string) (b []byte) {
	p := unsafe.Pointer((*reflect.StringHeader)(unsafe.Pointer(&s)).Data)
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	hdr.Data = uintptr(p)
	hdr.Cap = len(s)
	hdr.Len = len(s)
	return b
}
