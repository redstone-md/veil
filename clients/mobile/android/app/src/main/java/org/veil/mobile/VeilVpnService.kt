// Veil Android — VpnService.
//
// Owns the TUN file descriptor and the libveil session on the
// Android side. The JS layer talks to this service through
// VeilBridge (a ReactNativeModule) which sends start / stop intents
// and receives runtime events as broadcast LocalEvents.
//
// libveil itself is a JNI-callable shared library generated from
// `core/pkg/cgo` with `go build -buildmode=c-shared`. The native
// methods declared at the bottom of this file are bound at first
// use through System.loadLibrary("veil") and forward to the C ABI
// in core/pkg/cgo/include/veil.h.
//
// TUN routing strategy (Phase 4.6 v0):
//   1. Establish a TUN with a 10.x.x.x intra-tunnel address and
//      0.0.0.0/0 routes (full-tunnel), excluding the LAN ranges
//      operators usually want to reach directly.
//   2. Hand libveil the configuration text plus the TUN fd so the
//      Go side can run the SOCKS5 proxy on the loopback and
//      tun2socks on the fd.
//   3. (Future) replace the SOCKS5 + tun2socks bounce with a
//      direct VWP/1 TCP/UDP forwarder once libveil ships one.
//
// Many of the methods below are stubs that compile but do not
// fully exercise the libveil JNI surface yet — landing the file
// layout first lets the JS-facing UX iterate independently of the
// Go-side JNI work.

package org.veil.mobile

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log

class VeilVpnService : VpnService() {

    private var tun: ParcelFileDescriptor? = null
    private var sessionHandle: Long = 0L

    companion object {
        private const val TAG = "VeilVpnService"
        private const val NOTIF_CHANNEL = "veil-tunnel"
        // Arbitrary positive int unique inside our own package's
        // notification ID space; collisions with other apps don't
        // matter (each app has its own notification table).
        private const val NOTIF_ID = 0x5E11

        const val ACTION_START = "org.veil.mobile.START"
        const val ACTION_STOP  = "org.veil.mobile.STOP"
        const val EXTRA_CONFIG = "config_text"

        init {
            // libveil ships next to the APK as `libveil.so` per ABI
            // (arm64-v8a, armeabi-v7a, x86_64). gradle config in
            // android/app/build.gradle copies the produced binaries
            // into src/main/jniLibs/<abi>/ at assemble time.
            try {
                System.loadLibrary("veil")
            } catch (e: UnsatisfiedLinkError) {
                Log.e(TAG, "libveil not found in apk; tunnel will not start", e)
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startSession(intent.getStringExtra(EXTRA_CONFIG))
            ACTION_STOP  -> stopSession()
            else -> Log.w(TAG, "ignoring unknown action: ${intent?.action}")
        }
        return START_STICKY
    }

    private fun startSession(configText: String?) {
        if (configText.isNullOrBlank()) {
            Log.e(TAG, "start without config")
            stopSelf()
            return
        }
        if (sessionHandle != 0L) {
            Log.w(TAG, "session already running")
            return
        }

        // 1. open the TUN.
        val builder = Builder()
            .setSession("Veil")
            .addAddress("10.42.0.2", 24)
            .addRoute("0.0.0.0", 0)
            .addRoute("::", 0)
            .addDnsServer("1.1.1.1")
            .addDnsServer("9.9.9.9")
            .setMtu(1380)
            .setBlocking(false)
        // Exempt our own process so libveil can dial out without
        // looping the new TUN through itself.
        try {
            builder.addDisallowedApplication(packageName)
        } catch (_: Exception) {
            // addDisallowedApplication is unavailable on some OEM forks
        }
        val tunPfd = builder.establish()
        if (tunPfd == null) {
            Log.e(TAG, "VpnService.Builder.establish returned null; user revoked?")
            stopSelf()
            return
        }
        tun = tunPfd
        Log.i(TAG, "TUN established fd=${tunPfd.fd}")

        // 2. promote to foreground service so Android does not kill
        //    us when the JS app is backgrounded.
        startForeground(NOTIF_ID, buildNotification())

        // 3. start libveil with the TUN fd. The JNI side calls
        //    veil_create + veil_start, then sets up tun2socks.
        try {
            sessionHandle = nativeStart(configText, tunPfd.fd)
            Log.i(TAG, "libveil session started: handle=$sessionHandle")
        } catch (e: Throwable) {
            Log.e(TAG, "nativeStart failed", e)
            stopSession()
        }
    }

    private fun stopSession() {
        if (sessionHandle != 0L) {
            try {
                nativeStop(sessionHandle)
            } catch (e: Throwable) {
                Log.e(TAG, "nativeStop failed", e)
            }
            sessionHandle = 0L
        }
        try {
            tun?.close()
        } catch (_: Exception) {}
        tun = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun buildNotification(): Notification {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val ch = NotificationChannel(
                NOTIF_CHANNEL,
                "Veil tunnel",
                NotificationManager.IMPORTANCE_LOW,
            )
            ch.setShowBadge(false)
            nm.createNotificationChannel(ch)
        }
        val openAppIntent = packageManager.getLaunchIntentForPackage(packageName)
        val pi = openAppIntent?.let {
            PendingIntent.getActivity(this, 0, it, PendingIntent.FLAG_IMMUTABLE)
        }
        val builder = Notification.Builder(this, NOTIF_CHANNEL)
            .setContentTitle("Veil VPN")
            .setContentText("Tunnel running")
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setOngoing(true)
        if (pi != null) builder.setContentIntent(pi)
        return builder.build()
    }

    override fun onDestroy() {
        super.onDestroy()
        stopSession()
    }

    // --- JNI surface --------------------------------------------------

    /**
     * Returns a non-zero opaque handle on success. The implementation
     * (in core/pkg/cgo/jni_android.go, to be added) calls
     * veil_create + veil_start and additionally hooks tun2socks onto
     * tunFd so packets the OS writes flow through the libveil-spawned
     * SOCKS5 listener.
     */
    private external fun nativeStart(configText: String, tunFd: Int): Long

    private external fun nativeStop(handle: Long)
}
