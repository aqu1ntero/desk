package clip

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
#include <string.h>
#import <AppKit/AppKit.h>

// clipPNG is the general pasteboard's image as PNG, a TIFF (what most apps
// copy) converted; NULL when it holds none. The caller frees it.
static void *clipPNG(int *n) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		NSData *d = [pb dataForType:NSPasteboardTypePNG];
		if (d == nil) {
			NSData *t = [pb dataForType:NSPasteboardTypeTIFF];
			if (t != nil) {
				NSBitmapImageRep *r = [NSBitmapImageRep imageRepWithData:t];
				d = [r representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
			}
		}
		if (d == nil || d.length == 0 || d.length > 64<<20) {
			return NULL;
		}
		void *b = malloc(d.length);
		memcpy(b, d.bytes, d.length);
		*n = (int)d.length;
		return b;
	}
}
*/
import "C"

import "unsafe"

// Image is the clipboard's image as image/png, or nil when it holds none.
func Image() ([]byte, string, error) {
	var n C.int
	p := C.clipPNG(&n)
	if p == nil {
		return nil, "", nil
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoBytes(p, n), "image/png", nil
}
