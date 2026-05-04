// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

//go:build cgo && android

// JNI bridge for the Android VpnService.
//
// Java declares two native methods on org.veil.mobile.VeilVpnService:
//
//	private external fun nativeStart(configText: String, tunFd: Int): Long
//	private external fun nativeStop(handle: Long)
//
// JNI binds those by symbol name following the
// `Java_<pkg>_<class>_<method>` convention. The functions in this file
// match those names (with `_` replaced for `.` in the pkg path) and
// forward to the existing veil_create + veil_mobile_start_with_tun /
// veil_destroy entry points.
//
// We deliberately do NOT register an event callback from the JNI side
// — the Kotlin layer subscribes to events via a separate mechanism
// (the bridge module emits 'veil-event' to React Native by polling
// metricsJson; an event-callback variant lands in a follow-up).

package main

/*
#cgo android LDFLAGS: -llog
#include <jni.h>
#include <stdlib.h>
#include <string.h>

// Convenience: extract the C-string view of a jstring. Caller must
// release with (*env)->ReleaseStringUTFChars when done. Wrapped in
// an inline helper because the JNIEnv function-table dereference is
// awkward to spell from Go.
static inline const char* veil_jni_get_utf(JNIEnv* env, jstring s) {
    if (env == NULL || s == NULL) return NULL;
    return (*env)->GetStringUTFChars(env, s, NULL);
}
static inline void veil_jni_release_utf(JNIEnv* env, jstring s, const char* p) {
    if (env != NULL && s != NULL && p != NULL) {
        (*env)->ReleaseStringUTFChars(env, s, p);
    }
}
*/
import "C"

import (
	"unsafe"
)

// Java_org_veil_mobile_VeilVpnService_nativeStart
//
//export Java_org_veil_mobile_VeilVpnService_nativeStart
func Java_org_veil_mobile_VeilVpnService_nativeStart(
	env *C.JNIEnv,
	_ C.jobject,
	configText C.jstring,
	tunFd C.jint,
) C.jlong {
	cfgPtr := C.veil_jni_get_utf(env, configText)
	if cfgPtr == nil {
		return 0
	}
	defer C.veil_jni_release_utf(env, configText, cfgPtr)

	handle := veil_create(cfgPtr)
	if handle == 0 {
		return 0
	}
	rc := veil_mobile_start_with_tun(handle, tunFd, nil, unsafe.Pointer(nil))
	if rc != 0 {
		veil_destroy(handle)
		return 0
	}
	return C.jlong(handle)
}

// Java_org_veil_mobile_VeilVpnService_nativeStop
//
//export Java_org_veil_mobile_VeilVpnService_nativeStop
func Java_org_veil_mobile_VeilVpnService_nativeStop(_ *C.JNIEnv, _ C.jobject, handle C.jlong) {
	if handle == 0 {
		return
	}
	veil_destroy(C.uint64_t(handle))
}
