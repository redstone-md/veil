// Veil Android — React Native bridge.
//
// The JS layer talks to the VpnService through this module. We
// cannot start a VpnService directly from JS without first calling
// VpnService.prepare() and showing the system consent dialog; that
// flow happens here behind the start() Promise.

package org.veil.mobile

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.util.Log
import com.facebook.react.bridge.ActivityEventListener
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableNativeMap
import com.facebook.react.modules.core.DeviceEventManagerModule

class VeilBridgeModule(private val ctx: ReactApplicationContext) :
    ReactContextBaseJavaModule(ctx), ActivityEventListener {

    companion object {
        private const val TAG = "VeilBridge"
        private const val REQ_VPN_PREPARE = 4671
        private const val EVENT_NAME = "veil-event"
    }

    private var pendingStart: PendingStart? = null

    init { ctx.addActivityEventListener(this) }

    override fun getName(): String = "VeilBridge"

    @ReactMethod
    fun start(configText: String, promise: Promise) {
        val intent = VpnService.prepare(ctx)
        if (intent == null) {
            // Already prepared.
            launchService(configText)
            promise.resolve(null)
            return
        }
        val activity = currentActivity
        if (activity == null) {
            promise.reject("no_activity", "VeilBridge.start needs a foreground activity")
            return
        }
        pendingStart = PendingStart(configText, promise)
        activity.startActivityForResult(intent, REQ_VPN_PREPARE)
    }

    @ReactMethod
    fun stop(promise: Promise) {
        val intent = Intent(ctx, VeilVpnService::class.java).apply {
            action = VeilVpnService.ACTION_STOP
        }
        ctx.startService(intent)
        promise.resolve(null)
    }

    @ReactMethod
    fun metricsJson(promise: Promise) {
        // Hooked up in a follow-up commit; for now report a minimal
        // shape so the JS side does not have to handle nulls.
        promise.resolve("""{"running":false}""")
    }

    @ReactMethod
    fun libraryVersion(promise: Promise) {
        promise.resolve("""{"version":"unavailable","commit":"","date":""}""")
    }

    @ReactMethod
    fun addListener(eventName: String) { /* required for NativeEventEmitter */ }

    @ReactMethod
    fun removeListeners(count: Int)    { /* required for NativeEventEmitter */ }

    private fun launchService(configText: String) {
        val intent = Intent(ctx, VeilVpnService::class.java).apply {
            action = VeilVpnService.ACTION_START
            putExtra(VeilVpnService.EXTRA_CONFIG, configText)
        }
        ctx.startForegroundService(intent)
    }

    fun emitEvent(payload: Map<String, Any?>) {
        val map = WritableNativeMap()
        for ((k, v) in payload) when (v) {
            is Int    -> map.putInt(k, v)
            is Long   -> map.putDouble(k, v.toDouble())
            is Double -> map.putDouble(k, v)
            is Boolean -> map.putBoolean(k, v)
            is String -> map.putString(k, v)
            null      -> map.putNull(k)
            else      -> map.putString(k, v.toString())
        }
        ctx.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
            .emit(EVENT_NAME, map)
    }

    // --- ActivityEventListener ---

    override fun onActivityResult(activity: Activity?, requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode != REQ_VPN_PREPARE) return
        val pending = pendingStart ?: return
        pendingStart = null
        if (resultCode == Activity.RESULT_OK) {
            launchService(pending.configText)
            pending.promise.resolve(null)
        } else {
            pending.promise.reject("vpn_consent_denied", "user denied VPN consent dialog")
            Log.w(TAG, "VPN consent denied: resultCode=$resultCode")
        }
    }

    override fun onNewIntent(intent: Intent?) { /* unused */ }

    private data class PendingStart(val configText: String, val promise: Promise)
}
