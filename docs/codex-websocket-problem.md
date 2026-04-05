# Codex App-Server Websocket Compression Problem

## Summary

The Codex CLI `app-server` sends websocket frames with **non-standard RSV bits** that neither of Go's two main websocket libraries can handle. The first request works, but the second request fails because Codex starts sending compressed frames after the first message exchange.

## What happens

1. We start `codex app-server --listen ws://127.0.0.1:<port>`
2. We connect via websocket
3. `initialize` handshake succeeds
4. First `thread/start` + `turn/start` succeeds — response streams back
5. Second `thread/start` fails — the websocket read returns an error about unexpected RSV bits

## Error messages

### gorilla/websocket
```
websocket: RSV2 set
websocket: RSV2 set, RSV3 set, FIN not set on control
```

### nhooyr.io/websocket
```
received header with unexpected rsv bits set: true:true:true
```

## What RSV bits mean

In the websocket protocol (RFC 6455), each frame has three reserved bits: RSV1, RSV2, RSV3. These are normally all zero unless a negotiated extension uses them.

- **RSV1** is used by the standard `permessage-deflate` extension (RFC 7692) to indicate a compressed message
- **RSV2 and RSV3** are not used by any standard extension

Codex sets **all three** (RSV1+RSV2+RSV3), which means it's using a non-standard or proprietary compression/framing scheme.

## What we tried

| Library | Config | Result |
|---|---|---|
| gorilla/websocket | `EnableCompression: false` | First request works, second fails (RSV2 set) |
| gorilla/websocket | `EnableCompression: true` | First request works, second fails (RSV2 set, RSV3 set) |
| nhooyr.io/websocket | default | First request works, second fails (unexpected rsv bits) |
| nhooyr.io/websocket | `CompressionMode: CompressionContextTakeover` | First request works, second fails (unexpected rsv bits true:true:true) |

## Why the first request works

The first request likely uses uncompressed frames. After the first successful exchange, Codex's server-side enables compression for subsequent messages, which produces frames with RSV bits that our libraries reject.

## Options

1. **Custom websocket reader** — write a frame reader that accepts RSV1+RSV2+RSV3 and passes the payload through to a decompressor. Requires understanding what compression Codex uses (likely zstd or a custom scheme, not standard deflate).

2. **Use stdio instead** — works reliably but is single-channel (one request at a time). Requires serializing requests with a mutex.

3. **Patch gorilla/websocket** — fork and modify `advanceFrame()` in `conn.go` to not reject RSV2/RSV3 bits, treating all frames as uncompressed. Risky — if the frames ARE compressed, the data will be garbage.

4. **Report to OpenAI** — the Codex app-server's websocket implementation uses non-standard RSV bits which breaks interoperability with standard Go websocket libraries. The VS Code extension (TypeScript) may use a websocket library that's more permissive.

## Recommendation

Option 1 (custom transport) is the cleanest long-term solution. We need to inspect what Codex actually sends when RSV2+RSV3 are set — capture the raw frames and determine if it's zstd, brotli, or something else.

Option 2 (stdio with serialization) works now and unblocks testing. Use it as a stopgap while investigating the custom transport.
