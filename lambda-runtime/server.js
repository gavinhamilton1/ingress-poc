const http = require('http')
const fs = require('fs')
const path = require('path')
const crypto = require('crypto')

const PORT = process.env.PORT || 8080
const FUNCTION_NAME = process.env.FUNCTION_NAME || 'unnamed'
const OTEL_ENDPOINT = process.env.OTEL_EXPORTER_OTLP_ENDPOINT || 'http://jaeger:4318'

// --- Lightweight W3C Trace Context + OTLP Span Reporter ---
// No npm dependencies — implements just enough to create child spans
// and report them to Jaeger via OTLP/HTTP protobuf-free JSON endpoint

function randomHex(bytes) { return crypto.randomBytes(bytes).toString('hex') }

function parseTraceparent(header) {
  if (!header) return null
  const parts = header.split('-')
  if (parts.length < 4) return null
  return { version: parts[0], traceId: parts[1], parentSpanId: parts[2], flags: parts[3] }
}

function createSpan(name, traceCtx, attrs) {
  const spanId = randomHex(8)
  const traceId = traceCtx ? traceCtx.traceId : randomHex(16)
  const parentSpanId = traceCtx ? traceCtx.parentSpanId : ''
  const startNano = BigInt(Date.now()) * 1000000n

  return {
    spanId, traceId, parentSpanId, name, startNano,
    attrs: attrs || {},
    end: function () {
      this.endNano = BigInt(Date.now()) * 1000000n
      reportSpan(this)
    },
    traceparent: `00-${traceId}-${spanId}-01`,
  }
}

function reportSpan(span) {
  const payload = {
    resourceSpans: [{
      resource: {
        attributes: [
          { key: 'service.name', value: { stringValue: `lambda.${FUNCTION_NAME}` } },
          { key: 'service.version', value: { stringValue: '1.0.0' } },
          { key: 'deployment.environment', value: { stringValue: 'demo' } },
        ]
      },
      scopeSpans: [{
        scope: { name: `lambda.${FUNCTION_NAME}` },
        spans: [{
          traceId: span.traceId,
          spanId: span.spanId,
          parentSpanId: span.parentSpanId || undefined,
          name: span.name,
          kind: 2, // SERVER
          startTimeUnixNano: span.startNano.toString(),
          endTimeUnixNano: span.endNano.toString(),
          attributes: Object.entries(span.attrs).map(([k, v]) => ({
            key: k,
            value: typeof v === 'number'
              ? { intValue: v.toString() }
              : typeof v === 'boolean'
                ? { boolValue: v }
                : { stringValue: String(v) }
          })),
          status: { code: (span.attrs['http.status_code'] || 200) >= 400 ? 2 : 1 },
        }]
      }]
    }]
  }

  const body = JSON.stringify(payload)
  const url = new URL(OTEL_ENDPOINT + '/v1/traces')
  const req = http.request({
    hostname: url.hostname,
    port: url.port,
    path: url.pathname,
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) },
    timeout: 2000,
  })
  req.on('error', () => {}) // fire and forget
  req.end(body)
}

// --- Load user handler ---
let handler
try {
  let code
  if (process.env.FUNCTION_CODE) {
    code = process.env.FUNCTION_CODE
  } else if (fs.existsSync('/app/handler.js')) {
    code = fs.readFileSync('/app/handler.js', 'utf8')
  } else {
    code = `
      module.exports = async (req, res) => {
        res.json({
          message: 'Hello from Lambda!',
          function: '${FUNCTION_NAME}',
          path: req.path,
          method: req.method,
          timestamp: new Date().toISOString(),
          headers: req.headers,
        })
      }
    `
  }
  const tmpFile = path.join('/tmp', 'handler-' + Date.now() + '.js')
  fs.writeFileSync(tmpFile, code)
  handler = require(tmpFile)
} catch (e) {
  console.error('Failed to load handler:', e)
  handler = async (req, res) => {
    res.status(500).json({ error: 'Handler load failed', detail: e.message })
  }
}

// --- HTTP Server ---
const server = http.createServer(async (rawReq, rawRes) => {
  const url = new URL(rawReq.url, `http://localhost:${PORT}`)
  const startTime = Date.now()

  // Health check — no tracing
  if (url.pathname === '/health') {
    rawRes.writeHead(200, { 'Content-Type': 'application/json' })
    rawRes.end(JSON.stringify({ status: 'ok', service: 'lambda', function: FUNCTION_NAME }))
    return
  }

  // Parse incoming trace context
  const traceCtx = parseTraceparent(rawReq.headers['traceparent'])

  // Create a span for this lambda invocation
  const span = createSpan(`lambda.${FUNCTION_NAME}`, traceCtx, {
    'http.method': rawReq.method,
    'http.target': rawReq.url,
    'http.scheme': 'http',
    'lambda.function': FUNCTION_NAME,
    'lambda.cold_start': false,
    'auth.subject': rawReq.headers['x-auth-subject'] || '',
  })

  // Read body
  let body = ''
  for await (const chunk of rawReq) body += chunk

  // Build Express-like req
  const req = {
    method: rawReq.method,
    path: url.pathname,
    query: Object.fromEntries(url.searchParams),
    headers: rawReq.headers,
    body: body,
    get: (h) => rawReq.headers[h.toLowerCase()],
  }
  try { req.json = JSON.parse(body) } catch {}

  // Build Express-like res
  let statusCode = 200
  const resHeaders = {
    'Content-Type': 'application/json',
    'x-lambda-id': process.env.HOSTNAME || 'unknown',
    'traceparent': span.traceparent,
  }
  let responded = false
  const res = {
    status: (code) => { statusCode = code; return res },
    json: (data) => {
      if (responded) return
      responded = true
      span.attrs['http.status_code'] = statusCode
      span.attrs['http.response_size'] = JSON.stringify(data).length
      span.attrs['lambda.duration_ms'] = Date.now() - startTime
      span.end()
      rawRes.writeHead(statusCode, { ...resHeaders, 'Content-Type': 'application/json' })
      rawRes.end(JSON.stringify(data))
    },
    send: (data) => {
      if (responded) return
      responded = true
      span.attrs['http.status_code'] = statusCode
      span.attrs['lambda.duration_ms'] = Date.now() - startTime
      span.end()
      rawRes.writeHead(statusCode, resHeaders)
      rawRes.end(typeof data === 'string' ? data : JSON.stringify(data))
    },
    header: (k, v) => { resHeaders[k] = v; return res },
    set: (k, v) => { resHeaders[k] = v; return res },
  }

  try {
    await handler(req, res)
    if (!responded) {
      span.attrs['http.status_code'] = statusCode
      span.attrs['lambda.duration_ms'] = Date.now() - startTime
      span.end()
      rawRes.writeHead(statusCode, resHeaders)
      rawRes.end('{}')
    }
  } catch (e) {
    if (!responded) {
      span.attrs['http.status_code'] = 500
      span.attrs['error'] = true
      span.attrs['error.message'] = e.message
      span.attrs['lambda.duration_ms'] = Date.now() - startTime
      span.end()
      rawRes.writeHead(500, { 'Content-Type': 'application/json', 'traceparent': span.traceparent })
      rawRes.end(JSON.stringify({ error: 'Lambda execution error', detail: e.message }))
    }
  }
})

server.listen(PORT, () => console.log(`Lambda ${FUNCTION_NAME} listening on :${PORT} (OTEL -> ${OTEL_ENDPOINT})`))
