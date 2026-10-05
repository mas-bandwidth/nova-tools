//go:build darwin

package friend

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework AppKit
#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>
#import <AppKit/AppKit.h>

static int reachTrusted(void) {
	return AXIsProcessTrusted() ? 1 : 0;
}

static AXUIElementRef reachFindText(AXUIElementRef el, int depth) {
	if (el == NULL || depth > 12) {
		return NULL;
	}
	CFTypeRef role = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXRoleAttribute, &role) == kAXErrorSuccess && role != NULL) {
		if (CFGetTypeID(role) == CFStringGetTypeID()) {
			if (CFStringCompare(role, kAXTextAreaRole, 0) == kCFCompareEqualTo ||
				CFStringCompare(role, kAXTextFieldRole, 0) == kCFCompareEqualTo) {
				CFRelease(role);
				return (AXUIElementRef)CFRetain(el);
			}
		}
		CFRelease(role);
	}
	CFTypeRef kids = NULL;
	AXUIElementRef found = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, &kids) == kAXErrorSuccess && kids != NULL) {
		if (CFGetTypeID(kids) == CFArrayGetTypeID()) {
			CFIndex n = CFArrayGetCount(kids);
			for (CFIndex i = 0; i < n && found == NULL; i++) {
				found = reachFindText((AXUIElementRef)CFArrayGetValueAtIndex(kids, i), depth + 1);
			}
		}
		CFRelease(kids);
	}
	return found;
}

static int reachTypeAndSubmit(const char *bundle, const char *text) {
	if (!AXIsProcessTrusted()) {
		return 2;
	}
	if (bundle == NULL || text == NULL) {
		return 3;
	}
	int rc = 3;
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundle];
		NSArray *apps = bid == nil ? nil : [NSRunningApplication runningApplicationsWithBundleIdentifier:bid];
		if (apps == nil || [apps count] == 0) {
			return 3;
		}
		NSRunningApplication *app = [apps objectAtIndex:0];
		[app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
		pid_t pid = [app processIdentifier];
		AXUIElementRef root = AXUIElementCreateApplication(pid);
		if (root == NULL) {
			return 4;
		}
		AXUIElementRef field = NULL;
		CFTypeRef windows = NULL;
		if (AXUIElementCopyAttributeValue(root, kAXWindowsAttribute, &windows) == kAXErrorSuccess && windows != NULL) {
			if (CFGetTypeID(windows) == CFArrayGetTypeID()) {
				CFIndex n = CFArrayGetCount(windows);
				for (CFIndex i = 0; i < n && field == NULL; i++) {
					field = reachFindText((AXUIElementRef)CFArrayGetValueAtIndex(windows, i), 0);
				}
			}
			CFRelease(windows);
		}
		if (field == NULL) {
			field = reachFindText(root, 0);
		}
		CFRelease(root);
		if (field == NULL) {
			return 3;
		}
		CFStringRef value = CFStringCreateWithCString(NULL, text, kCFStringEncodingUTF8);
		if (value == NULL) {
			CFRelease(field);
			return 5;
		}
		AXError set = AXUIElementSetAttributeValue(field, kAXSelectedTextAttribute, value);
		if (set != kAXErrorSuccess) {
			set = AXUIElementSetAttributeValue(field, kAXValueAttribute, value);
		}
		CFRelease(value);
		CFRelease(field);
		if (set != kAXErrorSuccess) {
			return 5;
		}
		CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)36, true);
		CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)36, false);
		if (down == NULL || up == NULL) {
			if (down != NULL) CFRelease(down);
			if (up != NULL) CFRelease(up);
			return 6;
		}
		CGEventPostToPid(pid, down);
		CGEventPostToPid(pid, up);
		CFRelease(down);
		CFRelease(up);
		rc = 0;
	}
	return rc;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func defaultAXTrusted() bool { return C.reachTrusted() == 1 }

// typeAndSubmit types text into the composer of bundle and submits it with Return.
func typeAndSubmit(bundle, text string) error {
	b := C.CString(bundle)
	defer C.free(unsafe.Pointer(b))
	s := C.CString(text)
	defer C.free(unsafe.Pointer(s))
	switch int(C.reachTypeAndSubmit(b, s)) {
	case 0:
		return nil
	case 2:
		return ErrNoAccessibility
	case 3:
		return WindowSkip{Reason: "no-gui-window"}
	default:
		return fmt.Errorf("the accessibility submit failed")
	}
}
