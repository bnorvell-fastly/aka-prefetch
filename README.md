# Fastly Compute - Origin Assist Prefetch

A Fastly Compute@Edge service implementing [Akamai-compatible Origin Assist Prefetch](https://techdocs.akamai.com/adaptive-media-delivery/docs/origin-assist-prefetch) for adaptive media streaming (HLS/DASH).

## Overview

This service acts as an intelligent CDN edge that prefetches sequential media segments based on hints from the origin server. When a client requests a manifest or media segment, the origin can specify which objects should be prefetched next, allowing the edge to proactively warm the cache before the client requests them.

### Key Features

- **100% Akamai Spec Compliant** - Drop-in replacement for Akamai AMD prefetch
- **Non-Blocking** - Client responses are sent immediately; prefetching happens in background
- **Platform-Aware Rate Limiting** - Respects Fastly Compute's 32 backend request limit
- **Smart Path Resolution** - Handles both absolute and relative prefetch paths
- **Cache Optimization** - Preserves prefetch hints in cached responses

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           CLIENT REQUEST                                │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │
                                │ GET /playlist.m3u8
                                │
                    ┌───────────▼──────────┐
                    │   Fastly Compute     │
                    │   (Edge Service)     │
                    └───────────┬──────────┘
                                │
                                │ Add Header:
                                │ CDN-Origin-Assist-Prefetch-Enabled: 1
                                │
                    ┌───────────▼──────────┐
                    │   Origin Server      │
                    │   (Packaging/USP)    │
                    └───────────┬──────────┘
                                │
                                │ HTTP 200 OK
                                │ Content: playlist.m3u8
                                │ CDN-Origin-Assist-Prefetch-Path: segment-1.ts
                                │ CDN-Origin-Assist-Prefetch-Path: segment-2.ts
                                │ CDN-Origin-Assist-Prefetch-Path: segment-3.ts
                                │
                    ┌───────────▼──────────┐
                    │   Fastly Compute     │
                    │   Processing:        │
                    │   1. Parse headers   │
                    │   2. Build list      │
                    │   3. Resolve paths   │
                    └───────────┬──────────┘
                                │
                ┌───────────────┼───────────────┐
                │               │               │
    ┌───────────▼──────┐        │      ┌────────▼──────────┐
    │  Send Response   │        │      │  Background       │
    │  to Client       │        │      │  Prefetch Loop    │
    │  (w.Close())     │        │      │  (up to 24 objs)  │
    └───────────┬──────┘        │      └────────┬──────────┘
                │               │               │
                │               │   ┌───────────▼───────────┐
    ┌───────────▼──────┐        │   │ GET segment-1.ts      │
    │                  │        │   │ GET segment-2.ts      │
    │   Client Gets    │        │   │ GET segment-3.ts      │
    │   Response       │        │   └───────────┬───────────┘
    │   Immediately    │        │               │
    │                  │        │   ┌───────────▼───────────┐
    └──────────────────┘        │   │ Discard Bodies        │
                                │   │ (Cache Warmed)        │
                                │   └───────────────────────┘
                                │
                    ┌───────────▼──────────┐
                    │   Next Client        │
                    │   Request Gets       │
                    │   Cache Hit!         │
                    └──────────────────────┘
```

### Request Flow

1. **Client Request**: Player requests manifest/segment
2. **Header Injection**: Edge adds `CDN-Origin-Assist-Prefetch-Enabled: 1` header
3. **Origin Response**: Origin returns content + `CDN-Origin-Assist-Prefetch-Path` headers
4. **Immediate Response**: Client receives response via `w.Close()` and continues playback
5. **Background Prefetch**: Edge parses prefetch list and fetches up to 24 objects
6. **Cache Warming**: Prefetched objects populate cache for subsequent requests

## Protocol Specification

### Request Header

```
CDN-Origin-Assist-Prefetch-Enabled: 1
```

Sent by the edge to the origin to indicate prefetch capability.

### Response Headers

#### Single Object
```
CDN-Origin-Assist-Prefetch-Path: /path/to/segment-1.ts
```

#### Multiple Objects (Multiple Headers)
```
CDN-Origin-Assist-Prefetch-Path: /path/to/segment-1.ts
CDN-Origin-Assist-Prefetch-Path: /path/to/segment-2.ts
CDN-Origin-Assist-Prefetch-Path: /path/to/segment-3.ts
```

#### Multiple Objects (Comma-Separated)
```
CDN-Origin-Assist-Prefetch-Path: segment-1.ts, segment-2.ts, segment-3.ts
```

### Path Resolution

#### Absolute Paths
```
CDN-Origin-Assist-Prefetch-Path: /video/segment-1.ts
→ Resolves to: https://example.com/video/segment-1.ts
```

#### Relative Paths
```
Request: https://example.com/video/playlist.m3u8
CDN-Origin-Assist-Prefetch-Path: segment-1.ts
→ Resolves to: https://example.com/video/segment-1.ts
```

### Query Parameter Preservation

Query parameters from the original request are preserved in all prefetch requests:

```
Original: https://example.com/playlist.m3u8?token=abc123&session=xyz
Prefetch: https://example.com/segment-1.ts?token=abc123&session=xyz
```

This is critical for:
- Authentication tokens
- Session identifiers
- DRM parameters
- Analytics tracking

## Platform Constraints

### Fastly Compute Limits

- **Max Backend Requests**: 32 per invocation
- **Implementation Limit**: 24 prefetch objects (accounting for initial request)
- **Behavior**: Gracefully terminates prefetch loop when limit reached

### Debug Mode

Enable debug logging (via log tailing) by adding a request header:

```bash
curl -H "FASTLY-DEBUG: 1" https://your-edge.example.com/playlist.m3u8
```

Debug mode outputs:
- Service version and hostname
- Prefetch object list
- Individual fetch requests and status codes
- Exposes prefetch headers in client response (normally stripped)

## Deployment

### Prerequisites

- Fastly account with Compute@Edge enabled
- Go 1.17+
- TinyGo for WebAssembly compilation

### Build

```bash
fastly compute build
```

### Deploy

```bash
fastly compute deploy
```

### Service Configuration

1. Create a backend named `origin` pointing to your packaging server
2. (Optional) Enable caching with appropriate TTLs - cache control headers will be used, or Fastly defaults
3. (Optional) Configure backend specific timeouts 

## Testing

### Manual Testing

1. Set up an origin server that returns `CDN-Origin-Assist-Prefetch-Path` headers
2. Configure `fastly.toml` to point to your origin
3. Run locally: `fastly compute serve`
4. Make a request with debug enabled
5. Verify prefetch behavior in logs

### Expected Behavior

```
=> Service Version : 1 running on compute-12345 from 192.168.1.100 <=
=> Prefetching
=> fetching :  https://example.com/segment-1.ts?token=abc123
==> Got response 200 for object https://example.com/segment-1.ts?token=abc123
=> fetching :  https://example.com/segment-2.ts?token=abc123
==> Got response 200 for object https://example.com/segment-2.ts?token=abc123
```

## Implementation Details

### Memory Safety

All prefetched response bodies are immediately discarded to prevent memory accumulation:

```go
if err == nil && resp.StatusCode == fsthttp.StatusOK {
    io.Copy(io.Discard, resp.Body)
    resp.Body.Close()
}
```

### Error Handling

- **Parse Errors**: Logged and skipped (doesn't block other prefetches)
- **Backend Errors**: Counted against limit but doesn't fail client request
- **Connection Failures**: Initial request failure will return a 502; prefetches fail silently
- **Non-200 Responses**: Skipped

### Security

- **Method Filtering**: Only GET requests allowed (returns 405 otherwise)
- **Origin-Controlled Paths**: Prefetch URLs dictated by origin, not client
- **No Path Injection**: Paths constructed from validated origin headers
- **Query Preservation**: Original request parameters maintained (no modification)

### Cache Optimization

The implementation keeps the `CDN-Origin-Assist-Prefetch-Enabled` header on prefetch subrequests, allowing
subsequent requests to provide the prefetch headers, and continue prefetching the stream

**Benefits:**
- Cached responses retain prefetch metadata
- Subsequent cache hits can trigger their own prefetches
- Better cache efficiency without recursive loops (only initial response is processed)

## Performance Characteristics

### Latency Impact

- **Client Latency**: Zero (response sent before prefetch begins)
- **Cache Warming**: Sequential (up to 24 objects)
- **Memory Footprint**: Minimal (bodies immediately discarded)

### Scalability

- **Concurrent Requests**: Each invocation is independent
- **Backend Load**: Limited to 1 initial + 24 prefetch = 25 requests max
- **Cache Hit Rate**: Improved through proactive prefetching

### Origin Server Requirements

Must support and return:
- `CDN-Origin-Assist-Prefetch-Path` response headers
- Respond to `CDN-Origin-Assist-Prefetch-Enabled` request header

### Compatible Packaging Systems

- **Unified Streaming Platform (USP)**
- **Akamai Media Services**
- **AWS MediaPackage** (with custom header support)
- **Custom packagers** implementing the Akamai spec

## Troubleshooting

### Prefetch Not Triggering
1. Verify origin returns `CDN-Origin-Assist-Prefetch-Path` headers
2. Check debug logs: `curl -H "FASTLY-DEBUG: 1" ...`
3. Confirm origin sees `CDN-Origin-Assist-Prefetch-Enabled: 1` in request

### Backend Limit Errors
If seeing "Backend limit reached" frequently:
- Origin is returning too many prefetch paths
- Consider reducing prefetch suggestions at origin
- Current limit: 24 objects per request

### Cache Not Warming
1. Verify prefetch requests are succeeding (200 OK in debug logs)
2. Check cache TTL configuration
3. Confirm cache key consistency between client and prefetch requests

## Future Enhancements

### Planned Features

- [ ] **Config Store Integration**: Dynamic enable/disable via Config Store
- [ ] **Metrics Collection**: Built-in prefetch analytics
- [ ] **Parallel Prefetching**: Use goroutines for concurrent fetching
- [ ] **NonCacheable returns**: Ensure only status 200 returns are cached

## References

- [Akamai Origin Assist Prefetch Specification](https://techdocs.akamai.com/adaptive-media-delivery/docs/origin-assist-prefetch)
- [Unified Streaming Prefetch Headers](https://docs.unified-streaming.com/documentation/vod/prefetch_headers.html)

## Author

- **Brock Norvell** (bnorvell@fastly.com)