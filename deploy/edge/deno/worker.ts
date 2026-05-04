// Veil VPN — Deno Deploy edge worker.
//
// This worker accepts WSS upgrades on a configured path and proxies
// the binary websocket frames to an origin Veil server over a raw
// TCP connection. The Veil session above the WSS layer is end-to-end
// encrypted (Noise XK + AEAD), so the worker handles ciphertext only.
//
// Deploy:
//
//     deno deploy --project=<your-project> --prod worker.ts
//
// Environment variables:
//
//   VEIL_ORIGIN_HOST   the IP or DNS name of the origin VPS (required)
//   VEIL_ORIGIN_PORT   the inside-port of the origin's WSS listener
//                      (default 443)
//   VEIL_PATH          the URL path that accepts WSS upgrades
//                      (default "/ws"). Operators SHOULD pick a
//                      non-default path so untargeted scanners do not
//                      stumble onto the endpoint.
//
// The worker is intentionally small (~80 lines of TS): it does not
// authenticate, terminate Noise, or interpret any application bytes.
// It is also intentionally honest about what it cannot do — see
// ADR-0004 for the trust model.

const ORIGIN_HOST = Deno.env.get("VEIL_ORIGIN_HOST");
const ORIGIN_PORT = Number(Deno.env.get("VEIL_ORIGIN_PORT") ?? "443");
const WS_PATH     = Deno.env.get("VEIL_PATH") ?? "/ws";

if (!ORIGIN_HOST) {
  console.error("VEIL_ORIGIN_HOST is required");
  Deno.exit(1);
}

Deno.serve(async (req) => {
  const url = new URL(req.url);

  if (url.pathname !== WS_PATH) {
    // Generic 404 to avoid self-fingerprinting the deployment as a
    // Veil edge.
    return new Response("Not Found", { status: 404 });
  }
  if (req.headers.get("upgrade")?.toLowerCase() !== "websocket") {
    // The worker only handles WSS upgrades. Plain HTTP requests on
    // the path get the same 404 to avoid disclosing it.
    return new Response("Not Found", { status: 404 });
  }

  const { socket, response } = Deno.upgradeWebSocket(req, {
    protocol: "binary",
  });

  let origin: Deno.TcpConn | null = null;

  socket.binaryType = "arraybuffer";

  socket.onopen = async () => {
    try {
      origin = await Deno.connect({
        hostname: ORIGIN_HOST,
        port: ORIGIN_PORT,
      });
      pumpOriginToSocket(origin, socket);
    } catch (e) {
      console.error("origin connect:", e);
      try { socket.close(1011, "origin connect failed"); } catch { /* ignore */ }
    }
  };

  socket.onmessage = (ev) => {
    if (!origin) return;
    try {
      if (ev.data instanceof ArrayBuffer) {
        origin.write(new Uint8Array(ev.data));
      } else if (typeof ev.data === "string") {
        // Veil never sends text frames; ignore but log.
        console.warn("dropped text frame; Veil is binary-only");
      }
    } catch (e) {
      console.error("write origin:", e);
      try { socket.close(1011, "origin write failed"); } catch { /* ignore */ }
    }
  };

  const closeBoth = () => {
    try { origin?.close(); } catch { /* ignore */ }
    try { socket.close(); } catch { /* ignore */ }
  };

  socket.onclose = closeBoth;
  socket.onerror = closeBoth;

  return response;
});

// pumpOriginToSocket reads from the origin TCP connection in 16 KiB
// chunks and forwards every chunk as a single binary websocket
// frame. We do not try to coalesce or split; the Veil client and
// server above the WSS layer already handle their own framing.
async function pumpOriginToSocket(o: Deno.TcpConn, ws: WebSocket) {
  const buf = new Uint8Array(16 * 1024);
  try {
    while (true) {
      const n = await o.read(buf);
      if (n === null) {
        try { ws.close(1000, "origin EOF"); } catch { /* ignore */ }
        return;
      }
      // Slice creates a defensive copy so the Uint8Array we hand to
      // the websocket library does not get reused while the frame
      // is in flight.
      ws.send(buf.slice(0, n));
    }
  } catch (e) {
    console.error("read origin:", e);
    try { ws.close(1011, "origin read failed"); } catch { /* ignore */ }
  }
}
