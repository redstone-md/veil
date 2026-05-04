// Veil Android — ReactPackage registration.
//
// Add `VeilBridgePackage()` to the list returned from
// MainApplication.getPackages() so React Native picks the module up
// at startup. Expo bare projects do this through `react-native.config.js`
// + autolinking; if your build cannot find the bridge at runtime,
// confirm autolinking is on for this directory.

package org.veil.mobile

import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ViewManager

class VeilBridgePackage : ReactPackage {
    override fun createNativeModules(ctx: ReactApplicationContext): List<NativeModule> =
        listOf(VeilBridgeModule(ctx))

    override fun createViewManagers(ctx: ReactApplicationContext): List<ViewManager<*, *>> =
        emptyList()
}
