// Veil VPN — Node.js NAPI bindings.
//
// Thin wrapper over the safe Rust SDK (`veil` crate). The shape we
// expose to JS mirrors the Rust API but is JS-flavoured: a class
// with constructor / start / stop / metrics methods, plus an event
// callback parameter on start().
//
// The Veil event callback fires on a Go-owned goroutine; we cannot
// invoke a Node function from there directly. napi-rs's
// ThreadsafeFunction handles the cross-thread dispatch — we hand it
// the JS callback once on start() and call it from the C trampoline.

#![deny(clippy::all)]

use std::sync::{Arc, Mutex};

use napi::{
    bindgen_prelude::{Env, Error, Result, Status},
    threadsafe_function::{
        ThreadSafeCallContext, ThreadsafeFunction, ThreadsafeFunctionCallMode,
    },
};
use napi_derive::napi;
use veil::{Event, EventHandler, Veil as InnerVeil};

/// JS-facing Veil client. One instance per running session.
#[napi]
pub struct Veil {
    inner: Arc<Mutex<Option<InnerVeil>>>,
}

#[napi]
impl Veil {
    /// Construct a Veil client from JSON or YAML configuration text.
    /// Throws if the configuration is invalid.
    #[napi(constructor)]
    pub fn new(config_text: String) -> Result<Self> {
        let inner = InnerVeil::create(&config_text)
            .map_err(|e| Error::new(Status::InvalidArg, format!("{e}")))?;
        Ok(Veil {
            inner: Arc::new(Mutex::new(Some(inner))),
        })
    }

    /// Start the client. The optional callback receives one
    /// argument per event (a JS object matching the Veil Event
    /// shape: { type, message, transport, remote, bytes_tx,
    /// bytes_rx }).
    ///
    /// Throws if the instance has already been destroyed or if
    /// libveil rejects the start request.
    #[napi]
    pub fn start(&self, env: Env, callback: Option<napi::JsFunction>) -> Result<()> {
        let guard = self.inner.lock().map_err(poisoned)?;
        let v = guard
            .as_ref()
            .ok_or_else(|| Error::new(Status::InvalidArg, "instance has been destroyed"))?;

        let handler: Option<EventHandler> = match callback {
            None => None,
            Some(js_cb) => {
                // ThreadsafeFunction does the cross-thread marshal so
                // the libveil reporter goroutine can hand events to
                // the Node event loop without touching the JS engine
                // directly.
                let tsfn: ThreadsafeFunction<JsEvent, napi::threadsafe_function::ErrorStrategy::Fatal> =
                    js_cb.create_threadsafe_function(0, |ctx: ThreadSafeCallContext<JsEvent>| {
                        let env = ctx.env;
                        let mut obj = env.create_object()?;
                        obj.set("type", ctx.value.kind)?;
                        obj.set("message", ctx.value.message)?;
                        obj.set("transport", ctx.value.transport)?;
                        obj.set("remote", ctx.value.remote)?;
                        obj.set("bytes_tx", ctx.value.bytes_tx)?;
                        obj.set("bytes_rx", ctx.value.bytes_rx)?;
                        Ok(vec![obj])
                    })?;
                let cb: EventHandler = Arc::new(move |e: Event| {
                    tsfn.call(JsEvent::from(e), ThreadsafeFunctionCallMode::NonBlocking);
                });
                Some(cb)
            }
        };

        v.start(handler)
            .map_err(|e| Error::new(Status::GenericFailure, format!("{e}")))?;
        let _ = env;
        Ok(())
    }

    /// Request a graceful stop. Returns immediately; shutdown
    /// completes on a Veil-owned goroutine.
    #[napi]
    pub fn stop(&self) -> Result<()> {
        let guard = self.inner.lock().map_err(poisoned)?;
        let v = guard
            .as_ref()
            .ok_or_else(|| Error::new(Status::InvalidArg, "instance has been destroyed"))?;
        v.stop()
            .map_err(|e| Error::new(Status::GenericFailure, format!("{e}")))
    }

    /// Snapshot of the running session's metrics, returned as a
    /// JSON-encoded string the caller can JSON.parse if they want
    /// structured data. The string shape is { running, bytes_tx,
    /// bytes_rx }.
    #[napi(js_name = "metricsJson")]
    pub fn metrics_json(&self) -> Result<String> {
        let guard = self.inner.lock().map_err(poisoned)?;
        let v = guard
            .as_ref()
            .ok_or_else(|| Error::new(Status::InvalidArg, "instance has been destroyed"))?;
        let m = v
            .metrics()
            .map_err(|e| Error::new(Status::GenericFailure, format!("{e}")))?;
        serde_json::to_string(&m).map_err(|e| Error::new(Status::GenericFailure, format!("{e}")))
    }

    /// Tear the instance down explicitly. After this any further
    /// method call throws; otherwise destruction happens on GC.
    #[napi]
    pub fn destroy(&self) -> Result<()> {
        let mut guard = self.inner.lock().map_err(poisoned)?;
        guard.take(); // drop drives veil_stop + veil_destroy
        Ok(())
    }
}

/// Library version metadata. Free function rather than a class
/// method since it does not require a running instance.
#[napi(js_name = "libraryVersion")]
pub fn library_version() -> Result<String> {
    let v = InnerVeil::library_version()
        .map_err(|e| Error::new(Status::GenericFailure, format!("{e}")))?;
    serde_json::to_string(&v).map_err(|e| Error::new(Status::GenericFailure, format!("{e}")))
}

/// Owned event payload safe to ship across threads via
/// ThreadsafeFunction. Mirrors veil::Event field-for-field; we copy
/// the strings so the napi worker thread does not need to share
/// lifetime with the Veil internals.
struct JsEvent {
    kind: i32,
    message: String,
    transport: String,
    remote: String,
    bytes_tx: i64,
    bytes_rx: i64,
}

impl From<Event> for JsEvent {
    fn from(e: Event) -> Self {
        JsEvent {
            kind: e.kind,
            message: e.message,
            transport: e.transport,
            remote: e.remote,
            bytes_tx: e.bytes_tx,
            bytes_rx: e.bytes_rx,
        }
    }
}

fn poisoned<T>(_: std::sync::PoisonError<T>) -> Error {
    Error::new(Status::GenericFailure, "veil-node mutex poisoned")
}
