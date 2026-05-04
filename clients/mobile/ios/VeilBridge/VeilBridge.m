// Veil iOS — Objective-C glue exposing the Swift VeilBridge class
// to React Native's bridging machinery.
//
// React Native's RCT_EXTERN_MODULE expands into the Objective-C
// runtime registration RN looks up at startup; the actual
// implementation lives in VeilBridge.swift.

#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>

@interface RCT_EXTERN_MODULE(VeilBridge, RCTEventEmitter)

RCT_EXTERN_METHOD(start:(NSString *)configText
                  resolver:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(stop:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(metricsJson:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

RCT_EXTERN_METHOD(libraryVersion:(RCTPromiseResolveBlock)resolve
                  rejecter:(RCTPromiseRejectBlock)reject)

@end
